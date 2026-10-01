// 进房 ACL:回调 Go API 内部端点解析内容角色。
// fail-closed:回调失败或角色为空一律拒绝进房(Go 不可用时场景 GET
// 同样不可用,fail-open 无收益)。结果按连接缓存,TTL 不超过 token 有效期。
import type { Socket } from "socket.io";

import type { Config } from "./config";
import type { SocketData } from "./auth";

export type CanvasRole = "owner" | "editor" | "viewer";

const CACHE_TTL_MS = 60_000;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isCanvasRoom(roomId: string): boolean {
  return UUID_RE.test(roomId);
}

interface InternalAccessResponse {
  code: number;
  data?: { role?: string | null };
}

// resolveRole 返回 null 表示拒绝进房(无关系/链接失效/回调失败)。
export async function resolveRole(
  config: Config,
  socket: Socket,
  canvasId: string,
): Promise<CanvasRole | null> {
  const data = socket.data as SocketData;
  if (!data.acl) data.acl = new Map();

  const cached = data.acl.get(canvasId);
  const now = Date.now();
  if (cached && cached.expires > now) {
    return cached.role;
  }

  let role: CanvasRole | null = null;
  try {
    const resp = await fetch(
      `${config.goApiUrl}/internal/canvas/${encodeURIComponent(canvasId)}/access`,
      {
        headers: {
          Authorization: `Bearer ${data.token}`,
          "X-Internal-Token": config.internalToken,
        },
        signal: AbortSignal.timeout(5000),
      },
    );
    if (resp.ok) {
      const body = (await resp.json()) as InternalAccessResponse;
      if (body.code === 0) {
        const r = body.data?.role;
        if (r === "owner" || r === "editor" || r === "viewer") {
          role = r;
        }
      }
    }
  } catch {
    role = null;
  }

  const ttl = Math.min(CACHE_TTL_MS, Math.max(0, (data.tokenExp * 1000 - now) || CACHE_TTL_MS));
  data.acl.set(canvasId, { role, expires: now + ttl });
  return role;
}
