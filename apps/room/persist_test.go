package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testCanvas = "0b3f2a1c-1111-4222-8333-444455556666"

type fakeRows struct {
	pgx.Rows
	ids []int64
	idx int
	err error
}

func (f *fakeRows) Next() bool { return f.idx < len(f.ids) }
func (f *fakeRows) Scan(dest ...any) error {
	p, ok := dest[0].(*int64)
	if !ok {
		return errors.New("bad scan target")
	}
	*p = f.ids[f.idx]
	f.idx++
	return nil
}
func (f *fakeRows) Err() error { return f.err }
func (f *fakeRows) Close()     {}

// newTestPersister 用假 queryFn 驱动真实的排队/批组/退避/排水语义。
// pool 用真实未连接实例(pgxpool 懒连接,Close 不触网),避免 nil 解引用。
func newTestPersister(t *testing.T, ids []int64, insertErr error, record *[]string) *Persister {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://test@127.0.0.1:1/test")
	if err != nil {
		t.Fatalf("parse test db url: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	p := newPersisterWithPool(pool, 1000)
	p.batchWindow = time.Millisecond
	p.retryBase = time.Millisecond
	p.retryCap = 4 * time.Millisecond
	p.queryFn = func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
		if record != nil {
			*record = append(*record, sql)
		}
		if insertErr != nil {
			return nil, insertErr
		}
		return &fakeRows{ids: ids}, nil
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestPersisterAppendValidation(t *testing.T) {
	p := newTestPersister(t, []int64{1}, nil, nil)
	seqs := make(chan int64, 1)
	errs := make(chan error, 1)
	if p.Append("not-a-uuid", []byte(`[]`), func(s int64) { seqs <- s }, func(e error) { errs <- e }) {
		t.Error("non-uuid canvas must be rejected")
	}
	if p.Append(testCanvas, nil, func(s int64) { seqs <- s }, func(e error) { errs <- e }) {
		t.Error("empty elements must be rejected")
	}
	if !p.Append(testCanvas, []byte(`[{"id":"e1"}]`), func(s int64) { seqs <- s }, func(e error) { errs <- e }) {
		t.Error("valid append must be accepted")
	}
	select {
	case s := <-seqs:
		if s != 1 {
			t.Errorf("seq = %d, want 1", s)
		}
	case <-time.After(time.Second):
		t.Fatal("onSeq never called")
	}
}

func TestPersisterOverflowEvictsOldest(t *testing.T) {
	p := newTestPersister(t, nil, nil, nil)
	p.queueMax = 2
	p.batchWindow = time.Hour // flush 只留给 Close,append 全在测试协程内完成
	evictErrs := make(chan string, 4)
	for i := 0; i < 4; i++ {
		tag := string(rune('a' + i))
		if !p.Append(testCanvas, []byte(`["`+tag+`"]`), nil, func(e error) { evictErrs <- tag }) {
			t.Fatalf("append %d rejected", i)
		}
	}
	// 队列容量 2,第 3、4 条入队时逐出最旧两条;先到者(最早两条)必被调 onErr。
	first := <-evictErrs
	if first != "a" {
		t.Errorf("first evicted = %q, want a (oldest)", first)
	}
	second := <-evictErrs
	if second != "b" {
		t.Errorf("second evicted = %q, want b", second)
	}
	select {
	case got := <-evictErrs:
		t.Errorf("unexpected extra eviction: %q", got)
	default:
	}
	// 存活的应是最新的 c、d,由 Close 排水提交。
	p.Close()
	select {
	case got := <-evictErrs:
		t.Fatalf("survivor evicted on close: %q", got)
	default:
	}
}

func TestPersisterRetryAfterInsertError(t *testing.T) {
	attempts := 0
	done := make(chan int64, 1)
	p := newTestPersister(t, []int64{7}, nil, nil)
	p.retryBase = 5 * time.Millisecond
	p.queryFn = func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
		attempts++
		if attempts <= 2 {
			return nil, errors.New("boom")
		}
		return &fakeRows{ids: []int64{7}}, nil
	}
	p.Append(testCanvas, []byte(`[{"x":1}]`), func(s int64) { done <- s }, nil)
	seq := <-done
	if seq != 7 {
		t.Fatalf("seq = %d", seq)
	}
	if attempts < 3 {
		t.Errorf("attempts = %d, want >= 3 (2 failures then success)", attempts)
	}
}

func TestPersisterCloseDrains(t *testing.T) {
	var sqls []string
	p := newTestPersister(t, []int64{1, 2, 3}, nil, &sqls)
	p.batchWindow = time.Hour // 阻止定时 flush,全部留给 Close 排水
	errs := make(chan error, 4)
	for i := 0; i < 3; i++ {
		p.Append(testCanvas, []byte(`[1]`), nil, func(e error) { errs <- e })
	}
	p.Close()
	select {
	case err := <-errs:
		t.Fatalf("queued events must be drained on close, got err %v", err)
	default:
	}
	if len(sqls) != 1 {
		t.Fatalf("inserts = %d, want 1 (single batch of 3)", len(sqls))
	}
}

func TestPersisterBatchSQLShape(t *testing.T) {
	var sqls []string
	p := newTestPersister(t, []int64{}, nil, &sqls)
	p.batchWindow = time.Hour
	p.Append(testCanvas, []byte(`[1]`), nil, nil)
	p.Append(testCanvas, []byte(`[2]`), nil, nil)
	p.Close()
	if len(sqls) != 1 {
		t.Fatalf("sqls = %v", sqls)
	}
	want := "INSERT INTO canvas_events (canvas_id, elements) VALUES ($1,$2::jsonb),($3,$4::jsonb) RETURNING id"
	if sqls[0] != want {
		t.Errorf("sql = %q\nwant  %q", sqls[0], want)
	}
}

func TestPersisterAppendAfterClose(t *testing.T) {
	p := newTestPersister(t, nil, nil, nil)
	p.Close()
	if p.Append(testCanvas, []byte(`[1]`), nil, nil) {
		t.Error("append after close must be rejected")
	}
}
