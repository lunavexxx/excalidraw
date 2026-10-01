// 先落库后转发(persist-then-relay,协议 v2)。server-broadcast 到达即
// 微批 INSERT ... RETURNING id(canvas_events.id 即每画布 seq),落库成功
// 后按序回调转发 + ack。前提:补丁同步(scene-diff)的精确性依赖
// "帧携带的 seq 已真实落库"。代价:DB 故障时实时转发随队列阻塞(有界,
// 溢出丢最旧并告警);恢复后由客户端巡检 + scene-diff 补齐断档。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	batchSize     = 50
	batchWindow   = 5 * time.Millisecond
	retryInitial  = 250 * time.Millisecond
	retryMax      = 5 * time.Second
	insertTimeout = 10 * time.Second
)

type queuedEvent struct {
	canvasID string
	elements []byte // json.RawMessage,原样进 ::jsonb
	onSeq    func(seq int64)
	onErr    func(err error)
}

type Persister struct {
	pool     *pgxpool.Pool
	queueMax int

	// queryFn 抽出池查询,单测注入假实现;生产为 pool.Query。
	queryFn     func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	batchSize   int
	batchWindow time.Duration
	retryBase   time.Duration
	retryCap    time.Duration

	mu         sync.Mutex
	queue      []*queuedEvent
	flushTimer *time.Timer
	retryTimer *time.Timer
	draining   bool
	retryDelay time.Duration
	dropped    int
	closed     bool
}

func NewPersister(databaseURL string, queueMax int) (*Persister, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database_url: %w", err)
	}
	// 与 Node 版 Pool({max: 5}) 对齐;连接寿命沿用 api/store 的习惯值。
	cfg.MaxConns = 5
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}
	return newPersisterWithPool(pool, queueMax), nil
}

func newPersisterWithPool(pool *pgxpool.Pool, queueMax int) *Persister {
	return &Persister{
		pool:        pool,
		queueMax:    queueMax,
		queryFn:     pool.Query,
		batchSize:   batchSize,
		batchWindow: batchWindow,
		retryBase:   retryInitial,
		retryCap:    retryMax,
		retryDelay:  retryInitial,
	}
}

// Append 校验并入队;落库成功后按队列序回调 onSeq(调用方据此转发 +
// ack),失败回调 onErr。返回 false 表示载荷非法或已关闭,不应转发。
func (p *Persister) Append(canvasID string, elements []byte, onSeq func(int64), onErr func(error)) bool {
	if !IsCanvasRoom(canvasID) || len(elements) == 0 {
		return false
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	var evicted *queuedEvent
	if len(p.queue) >= p.queueMax {
		evicted = p.queue[0]
		p.queue = p.queue[1:]
		p.dropped++
		if p.dropped%100 == 1 {
			log.Printf("[persist] queue overflow, dropped=%d", p.dropped)
		}
	}
	p.queue = append(p.queue, &queuedEvent{canvasID: canvasID, elements: elements, onSeq: onSeq, onErr: onErr})
	p.mu.Unlock()
	if evicted != nil && evicted.onErr != nil {
		evicted.onErr(errors.New("persist queue overflow"))
	}
	p.scheduleFlush()
	return true
}

// 微批:凑满 batchSize 或首条入队后 batchWindow 内触发,
// 单条多值 INSERT 一次拿整批真实 id(延迟上限 ≈ 一个批窗口)。
func (p *Persister) scheduleFlush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.flushTimer != nil || p.draining {
		return
	}
	p.flushTimer = time.AfterFunc(p.batchWindow, p.drain)
}

func (p *Persister) drain() {
	p.mu.Lock()
	p.flushTimer = nil
	if p.draining {
		p.mu.Unlock()
		return
	}
	p.draining = true
	p.mu.Unlock()

	for {
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.draining = false
			p.mu.Unlock()
			return
		}
		n := len(p.queue)
		if n > p.batchSize {
			n = p.batchSize
		}
		batch := p.queue[:n]
		p.queue = p.queue[n:]
		p.mu.Unlock()

		if err := p.insertBatch(batch); err != nil {
			log.Printf("[persist] insert failed, will retry: %v", err)
			p.mu.Lock()
			p.queue = append(batch, p.queue...) // 整批回退队首(保持到达序)
			p.draining = false
			p.mu.Unlock()
			p.scheduleRetry()
			return
		}
		p.mu.Lock()
		p.retryDelay = p.retryBase
		p.mu.Unlock()
	}
}

func (p *Persister) scheduleRetry() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retryTimer != nil {
		return
	}
	p.retryTimer = time.AfterFunc(p.retryDelay, func() {
		p.mu.Lock()
		p.retryTimer = nil
		p.retryDelay = min(p.retryDelay*2, p.retryCap)
		p.mu.Unlock()
		p.drain()
	})
}

func (p *Persister) insertBatch(batch []*queuedEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("INSERT INTO canvas_events (canvas_id, elements) VALUES ")
	args := make([]any, 0, len(batch)*2)
	for i, ev := range batch {
		if i > 0 {
			sb.WriteByte(',')
		}
		// 单语句原子;RETURNING 按 VALUES 顺序返回,与 batch 一一对应。
		fmt.Fprintf(&sb, "($%d,$%d::jsonb)", i*2+1, i*2+2)
		args = append(args, ev.canvasID, string(ev.elements))
	}
	sb.WriteString(" RETURNING id")

	rows, err := p.queryFn(ctx, sb.String(), args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	i := 0
	for rows.Next() && i < len(batch) {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if batch[i].onSeq != nil {
			batch[i].onSeq(id)
		}
		i++
	}
	return rows.Err()
}

// Close 优雅退出:先尽力清空队列再关闭连接池(调用方已先停收新事件)。
func (p *Persister) Close() {
	p.mu.Lock()
	if p.flushTimer != nil {
		p.flushTimer.Stop()
		p.flushTimer = nil
	}
	if p.retryTimer != nil {
		p.retryTimer.Stop()
		p.retryTimer = nil
	}
	p.closed = true
	queue := p.queue
	p.queue = nil
	p.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for len(queue) > 0 && time.Now().Before(deadline) {
		n := len(queue)
		if n > p.batchSize {
			n = p.batchSize
		}
		batch := queue[:n]
		queue = queue[n:]
		if err := p.insertBatch(batch); err != nil {
			break
		}
	}
	if len(queue) > 0 {
		log.Printf("[persist] close with %d events not persisted", len(queue))
		for _, ev := range queue {
			if ev.onErr != nil {
				ev.onErr(errors.New("persister closed"))
			}
		}
	}
	p.pool.Close()
}
