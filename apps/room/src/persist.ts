// persist.ts:先落库后转发(persist-then-relay,协议 v2)。
// server-broadcast 到达即微批 INSERT ... RETURNING id(canvas_events.id
// 即每画布 seq),落库成功后按序 resolve 给调用方转发 + ack。
// 前提:补丁同步(scene-diff)的精确性依赖"帧携带的 seq 已真实落库"。
// 代价:DB 故障时实时转发随队列阻塞(有界,溢出丢最旧并告警);
// 恢复后由客户端巡检 + scene-diff 补齐断档。
import { Pool } from "pg";

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const BATCH_SIZE = 50;
const BATCH_WINDOW_MS = 5;

interface QueuedEvent {
  canvasId: string;
  elementsJson: string;
  resolve: (seq: number) => void;
  reject: (err: Error) => void;
}

export interface BroadcastMessage {
  type: string;
  payload: { elements?: unknown };
  [key: string]: unknown;
}

// 解码线上载荷:客户端 emit 的是 TextEncoder 编码的 Uint8Array
// (socket.io 送达为 Buffer/ArrayBuffer),必须先解码再按 JSON 解析;
// 兼容直接传对象形态(dev/测试)。非场景消息返回 null。
export function decodeBroadcastPayload(data: unknown): BroadcastMessage | null {
  try {
    let msg: unknown = data;
    if (typeof data === "string") {
      msg = JSON.parse(data);
    } else if (Buffer.isBuffer(data) || data instanceof ArrayBuffer || ArrayBuffer.isView(data)) {
      const bytes =
        data instanceof ArrayBuffer
          ? new Uint8Array(data)
          : ArrayBuffer.isView(data)
            ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
            : data;
      msg = JSON.parse(new TextDecoder().decode(bytes));
    }
    if (!msg || typeof msg !== "object") return null;
    const m = msg as { type?: unknown; payload?: unknown };
    if (typeof m.type !== "string" || !m.payload || typeof m.payload !== "object") return null;
    return m as BroadcastMessage;
  } catch {
    return null;
  }
}

// 编码转发帧:与客户端 wire 格式一致(Uint8Array JSON),附加 seq。
export function encodeBroadcastPayload(msg: object): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(msg));
}

export class EventPersister {
  private pool: Pool;
  private queue: QueuedEvent[] = [];
  private flushTimer: NodeJS.Timeout | null = null;
  private draining = false;
  private retryTimer: NodeJS.Timeout | null = null;
  private retryDelay = 250;
  private dropped = 0;
  private staleDropped = 0;

  constructor(databaseURL: string, private queueMax: number) {
    this.pool = new Pool({ connectionString: databaseURL, max: 5 });
    this.pool.on("error", (err) => {
      console.error("[persist] pool error:", err.message);
    });
  }

  stats(): { queued: number; dropped: number } {
    return { queued: this.queue.length, dropped: this.dropped + this.staleDropped };
  }

  // append 校验并入队;resolve(seq) 在落库成功后触发(调用方据此
  // 转发 + ack)。返回 null 表示载荷非法(非场景增量),不应转发。
  append(canvasId: string, elements: unknown[]): Promise<number> | null {
    if (!UUID_RE.test(canvasId) || elements.length === 0) return null;
    return new Promise<number>((resolve, reject) => {
      const event: QueuedEvent = {
        canvasId,
        elementsJson: JSON.stringify(elements),
        resolve,
        reject,
      };
      if (this.queue.length >= this.queueMax) {
        const evicted = this.queue.shift();
        evicted?.reject(new Error("persist queue overflow"));
        this.dropped += 1;
        if (this.dropped % 100 === 1) {
          console.warn(`[persist] queue overflow, dropped=${this.dropped}`);
        }
      }
      this.queue.push(event);
      this.scheduleFlush();
    });
  }

  // 微批:凑满 BATCH_SIZE 或 BATCH_WINDOW_MS 内的首条即触发,
  // 单条多值 INSERT 一次拿整批真实 id(延迟上限 ≈ 一个批窗口)。
  private scheduleFlush(): void {
    if (this.flushTimer || this.draining) return;
    this.flushTimer = setTimeout(() => {
      this.flushTimer = null;
      void this.drain();
    }, BATCH_WINDOW_MS);
  }

  private async drain(): Promise<void> {
    this.draining = true;
    try {
      for (;;) {
        if (this.queue.length === 0) return;
        const batch = this.queue.splice(0, BATCH_SIZE);
        try {
          await this.insertBatch(batch);
          this.retryDelay = 250;
        } catch (err) {
          // 整批回退队首(保持到达序),等待退避重试。
          this.queue.unshift(...batch);
          console.error("[persist] insert failed, will retry:", (err as Error).message);
          this.scheduleRetry();
          return;
        }
      }
    } finally {
      this.draining = false;
      if (this.queue.length > 0 && !this.retryTimer) {
        this.scheduleFlush();
      }
    }
  }

  private scheduleRetry(): void {
    if (this.retryTimer) return;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.retryDelay = Math.min(this.retryDelay * 2, 5000);
      void this.drain();
    }, this.retryDelay);
  }

  private async insertBatch(batch: QueuedEvent[]): Promise<void> {
    const client = await this.pool.connect();
    try {
      // 单语句原子;RETURNING 按VALUES 顺序返回,与 batch 一一对应。
      const values: string[] = [];
      const params: unknown[] = [];
      batch.forEach((ev, i) => {
        values.push(`($${i * 2 + 1}, $${i * 2 + 2}::jsonb)`);
        params.push(ev.canvasId, ev.elementsJson);
      });
      const res = await client.query(
        `INSERT INTO canvas_events (canvas_id, elements) VALUES ${values.join(",")} RETURNING id`,
        params,
      );
      batch.forEach((ev, i) => ev.resolve(Number(res.rows[i].id)));
    } finally {
      client.release();
    }
  }

  // close 优雅退出:先尽力清空队列再关闭连接池。
  async close(): Promise<void> {
    if (this.flushTimer) {
      clearTimeout(this.flushTimer);
      this.flushTimer = null;
    }
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
    const deadline = Date.now() + 5000;
    while (this.queue.length > 0 && Date.now() < deadline) {
      const batch = this.queue.splice(0, BATCH_SIZE);
      try {
        await this.insertBatch(batch);
      } catch {
        break;
      }
    }
    if (this.queue.length > 0) {
      this.staleDropped += this.queue.length;
      console.warn(`[persist] close with ${this.queue.length} events not persisted`);
      for (const ev of this.queue) {
        ev.reject(new Error("persister closed"));
      }
      this.queue = [];
    }
    await this.pool.end();
  }
}
