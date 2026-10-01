package main

import "testing"

func TestRateLimiterInitialBurst(t *testing.T) {
	r := NewRateLimiter(3)
	now := int64(1000)
	for i := 0; i < 3; i++ {
		if !r.Allow("s1", now) {
			t.Fatalf("burst token %d denied", i)
		}
	}
	if r.Allow("s1", now) {
		t.Error("empty bucket must deny")
	}
	if !r.Allow("s2", now) {
		t.Error("other socket must have own bucket")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	r := NewRateLimiter(2) // 2 tokens/s
	now := int64(1000)
	for i := 0; i < 2; i++ {
		r.Allow("s1", now)
	}
	if r.Allow("s1", now+400) {
		t.Error("no refill after 0.4s at 2/s")
	}
	if !r.Allow("s1", now+600) {
		t.Error("refill after 0.6s at 2/s")
	}
	// 补充不超容量:闲置 100s 后仍只有 2 个令牌。
	for i := 0; i < 2; i++ {
		if !r.Allow("s1", now+100_000) {
			t.Fatalf("idle refill token %d denied", i)
		}
	}
	if r.Allow("s1", now+100_000) {
		t.Error("refill must not exceed capacity")
	}
}

func TestRateLimiterRelease(t *testing.T) {
	r := NewRateLimiter(1)
	now := int64(0)
	r.Allow("s1", now)
	if r.Allow("s1", now) {
		t.Fatal("bucket should be empty")
	}
	r.Release("s1")
	if !r.Allow("s1", now) {
		t.Error("released bucket must start fresh")
	}
}

func TestConnectionGuard(t *testing.T) {
	g := NewConnectionGuard(2)
	if !g.Acquire("u1") || !g.Acquire("u1") {
		t.Fatal("acquire under limit")
	}
	if g.Acquire("u1") {
		t.Error("acquire over limit")
	}
	if !g.Acquire("u2") {
		t.Error("other identity unaffected")
	}
	g.Release("u1")
	if !g.Acquire("u1") {
		t.Error("slot freed after release")
	}
	g.Release("u1")
	g.Release("u1") // 多余释放不产生负数
	if !g.Acquire("u1") || !g.Acquire("u1") {
		t.Error("counter must be consistent after over-release")
	}
}
