// guard.ts:每 socket 令牌桶限速 + 每身份并发连接数上限(基础滥用防护)。

export interface RateLimiter {
  allow(socketId: string): boolean;
  // 断开时释放桶,防重连累积泄漏(重连必得新 socket.id)。
  release(socketId: string): void;
}

// createRateLimiter 每连接一个令牌桶;超限丢弃(场景消息有 20s 全量
// 重播兜底,presence 是 volatile 本就可丢)。
export function createRateLimiter(perSecond: number): RateLimiter {
  const buckets = new Map<string, { tokens: number; last: number }>();
  return {
    allow(socketId: string): boolean {
      const now = Date.now();
      let bucket = buckets.get(socketId);
      if (!bucket) {
        bucket = { tokens: perSecond, last: now };
        buckets.set(socketId, bucket);
        return true;
      }
      bucket.tokens = Math.min(
        perSecond,
        bucket.tokens + ((now - bucket.last) / 1000) * perSecond,
      );
      bucket.last = now;
      if (bucket.tokens < 1) {
        return false;
      }
      bucket.tokens -= 1;
      return true;
    },
    release(socketId: string): void {
      buckets.delete(socketId);
    },
  };
}

// ConnectionGuard 限制单身份(access userId / guest subject)并发连接数。
// 键必须用稳定身份而非 IP:IP 在反代/NAT 后不唯一。
export class ConnectionGuard {
  private counts = new Map<string, number>();

  constructor(private limit: number) {}

  acquire(ip: string): boolean {
    const current = this.counts.get(ip) ?? 0;
    if (current >= this.limit) {
      return false;
    }
    this.counts.set(ip, current + 1);
    return true;
  }

  release(ip: string): void {
    const current = this.counts.get(ip) ?? 0;
    if (current <= 1) {
      this.counts.delete(ip);
    } else {
      this.counts.set(ip, current - 1);
    }
  }
}
