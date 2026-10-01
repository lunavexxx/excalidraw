// 握手鉴权:验签 JWT(HS256,与 apps/api 共享密钥),区分 access(登录)
// 与 guest(分享链接)两种身份;无效即拒绝连接(fail-closed)。
import type { Socket } from "socket.io";
import jwt from "jsonwebtoken";

import type { Config } from "./config";

export interface GuestIdentity {
  subject: string;
  shareLinkId: string;
  canvasId: string;
  role: "editor" | "viewer";
}

export interface SocketData {
  token: string;
  tokenExp: number; // epoch 秒
  userId?: string;
  guest?: GuestIdentity;
  role?: "owner" | "editor" | "viewer"; // join-room 时经 ACL 回调写入
  acl?: Map<string, { role: "owner" | "editor" | "viewer" | null; expires: number }>;
}

// access 与 api 的 HS256 claims 对齐:仅 typ/sub/iat/exp;
// guest 追加 cid/role,sub 形如 g:<shareLinkId>:<jti>。
export function authenticate(config: Config, token: string): SocketData | null {
  let payload: Record<string, unknown>;
  try {
    payload = jwt.verify(token, config.jwtSecret, {
      algorithms: ["HS256"],
    }) as Record<string, unknown>;
  } catch {
    return null;
  }
  if (typeof payload !== "object" || payload === null) return null;
  const exp = typeof payload.exp === "number" ? payload.exp : 0;

  if (payload.typ === "access" && typeof payload.sub === "string" && payload.sub) {
    return { token, tokenExp: exp, userId: payload.sub };
  }
  if (
    payload.typ === "guest" &&
    typeof payload.cid === "string" &&
    payload.cid &&
    (payload.role === "editor" || payload.role === "viewer") &&
    typeof payload.sub === "string"
  ) {
    const shareLinkId = parseGuestSubject(payload.sub);
    if (!shareLinkId) return null;
    return {
      token,
      tokenExp: exp,
      guest: { subject: payload.sub, shareLinkId, canvasId: payload.cid, role: payload.role },
    };
  }
  return null;
}

function parseGuestSubject(sub: string): string | null {
  if (!sub.startsWith("g:")) return null;
  const rest = sub.slice(2);
  const idx = rest.indexOf(":");
  if (idx <= 0 || idx === rest.length - 1) return null;
  return rest.slice(0, idx);
}

// guestSubOf 供测试/调试还原 guest subject。
export function guestSubOf(socket: Socket): string | null {
  return socket.data.guest?.subject ?? null;
}
