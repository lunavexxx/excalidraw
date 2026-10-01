// sync.ts:状态补丁(协议 v2 Sync Step 2)——room 收到客户端
// sync-request 后,回调 Go API 内部端点 scene-diff,从共享事件流
// (canvas_events)取 after 之后的事件折叠差异。room 不持有状态、
// 不复制折叠规则,API 是唯一事实源。
import type { Config } from "./config";

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface ScenePatch {
  elements: unknown[];
  cursor: number;
  has_more: boolean;
}

interface InternalDiffResponse {
  code: number;
  data?: { elements?: unknown[]; cursor?: number; has_more?: boolean };
}

// fetchSceneDiff 返回 null 表示失败(调用方回 ack error,客户端走 HTTP 兜底)。
export async function fetchSceneDiff(
  config: Config,
  canvasId: string,
  afterSeq: number,
  limit: number,
): Promise<ScenePatch | null> {
  if (!UUID_RE.test(canvasId) || !Number.isSafeInteger(afterSeq) || afterSeq < 0) {
    return null;
  }
  try {
    const resp = await fetch(
      `${config.goApiUrl}/internal/canvas/${encodeURIComponent(canvasId)}/scene-diff?after=${afterSeq}&limit=${limit}`,
      {
        headers: { "X-Internal-Token": config.internalToken },
        signal: AbortSignal.timeout(5000),
      },
    );
    if (!resp.ok) return null;
    const body = (await resp.json()) as InternalDiffResponse;
    if (body.code !== 0 || !Array.isArray(body.data?.elements)) return null;
    return {
      elements: body.data!.elements ?? [],
      cursor: Number(body.data!.cursor ?? afterSeq) || afterSeq,
      has_more: body.data!.has_more === true,
    };
  } catch {
    return null;
  }
}

export function parseAfterSeq(payload: unknown): number {
  const after = (payload as { after_seq?: unknown } | null)?.after_seq;
  return typeof after === "number" && Number.isSafeInteger(after) && after >= 0 ? after : 0;
}
