// 基础滥用防护:每 socket 令牌桶限速 + 每身份并发连接数上限。
package main

import "sync"

// RateLimiter 每连接一个令牌桶;超限静默丢弃(场景消息由断线后
// sync-request 补齐,presence 是 volatile 本就可丢)。
type RateLimiter struct {
	perSecond float64
	mu        sync.Mutex
	buckets   map[string]*bucket
}

type bucket struct {
	tokens float64
	last   int64 // ms
}

func NewRateLimiter(perSecond int) *RateLimiter {
	return &RateLimiter{perSecond: float64(perSecond), buckets: make(map[string]*bucket)}
}

// Allow 消耗一个令牌;桶随连接首次出现(首令牌即扣减,容量恒定),
// 断开时 Release 防泄漏(重连必得新 socketId)。
func (r *RateLimiter) Allow(socketID string, now int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[socketID]
	if !ok {
		r.buckets[socketID] = &bucket{tokens: r.perSecond - 1, last: now}
		return true
	}
	refill := float64(now-b.last) / 1000 * r.perSecond
	b.tokens = min(r.perSecond, b.tokens+refill)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (r *RateLimiter) Release(socketID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.buckets, socketID)
}

// ConnectionGuard 限制单身份(access userId / guest subject)并发连接数。
// 键必须用稳定身份而非 IP:IP 在反代/NAT 后不唯一。
type ConnectionGuard struct {
	limit  int
	mu     sync.Mutex
	counts map[string]int
}

func NewConnectionGuard(limit int) *ConnectionGuard {
	return &ConnectionGuard{limit: limit, counts: make(map[string]int)}
}

func (g *ConnectionGuard) Acquire(identity string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.counts[identity] >= g.limit {
		return false
	}
	g.counts[identity]++
	return true
}

func (g *ConnectionGuard) Release(identity string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.counts[identity] <= 1 {
		delete(g.counts, identity)
	} else {
		g.counts[identity]--
	}
}
