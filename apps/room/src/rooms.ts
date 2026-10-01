// rooms.ts:协议核心(上游 excalidraw-room 事件语义 + 本项目增改)。
// 上游事件全集:emit join-room/server-broadcast/server-volatile-broadcast/user-follow;
// on init-room/new-user/room-user-change/client-broadcast/first-in-room/user-follow-room-change。
// 增改:join-room 带 ack 回传角色;拒绝时 emit join-room-error(fail-closed);
// viewer 的 server-broadcast 被服务端丢弃(共享编辑仅及内容的强制点);
// 协议 v2:场景消息先落库后转发(帧携带 seq),新增 sync-request 状态补丁。
import type { Server, Socket } from "socket.io";

import type { Config } from "./config";
import type { SocketData } from "./auth";
import { isCanvasRoom, resolveRole } from "./acl";
import type { EventPersister } from "./persist";
import { decodeBroadcastPayload, encodeBroadcastPayload } from "./persist";
import { fetchSceneDiff, parseAfterSeq } from "./sync";
import type { ConnectionGuard, RateLimiter } from "./guard";

const followRoomPrefix = "follow@";
// 单次补丁窗口上限,与 Go API maxReadFoldEvents 对齐(has_more 分页)。
const syncDiffLimit = 2000;

export function registerRoomHandlers(io: Server, config: Config, persister: EventPersister, rateLimit: RateLimiter, connections: ConnectionGuard): void {
  io.on("connection", (socket) => {
    // 连接数按握手身份计(access userId / guest subject):IP 在反代/NAT
    // 后不唯一,不可作限制键;身份由握手鉴权保证存在(fail-closed)。
    const identity = connectionIdentity(socket);
    if (!connections.acquire(identity)) {
      socket.disconnect(true);
      return;
    }
    socket.on("disconnect", () => {
      connections.release(identity);
      rateLimit.release(socket.id);
    });

    socket.emit("init-room");

    socket.on("join-room", async (roomId: unknown, ack?: (result: unknown) => void) => {
      if (typeof roomId !== "string" || !isCanvasRoom(roomId)) {
        deny(socket, ack, "invalid_room");
        return;
      }
      if (!rateLimit.allow(socket.id)) {
        deny(socket, ack, "rate_limited");
        return;
      }
      const role = await resolveRole(config, socket, roomId);
      if (!role) {
        deny(socket, ack, "forbidden");
        return;
      }
      (socket.data as SocketData).role = role;
      socket.join(roomId);

      // 判空与名册均经 redis adapter 聚合(多实例语义)。
      const sockets = await io.in(roomId).fetchSockets();
      if (sockets.length <= 1) {
        socket.emit("first-in-room");
      } else {
        socket.to(roomId).emit("new-user", socket.id);
      }
      await broadcastRoster(io, roomId);
      if (typeof ack === "function") {
        ack({ role });
      }
    });

    socket.on("server-broadcast", (roomId: unknown, data?: unknown, ack?: (result: unknown) => void) => {
      if (typeof roomId !== "string") return;
      // 只转发/落库自己已通过 ACL 加入的画布房间(viewer 一律丢弃:
      // 场景更新只走本事件,presence 走 volatile,分叉即强制点)。
      if ((socket.data as SocketData).role === "viewer" || !socket.rooms.has(roomId)) return;
      if (!rateLimit.allow(socket.id)) return;
      // 协议 v2:解码二进制 JSON 帧 → 只认 SCENE_UPDATE 增量 →
      // persist-then-relay(帧携带落库 seq,先落库后转发);
      // 落库失败不转发,客户端巡检 + scene-diff 自愈。
      const msg = decodeBroadcastPayload(data);
      if (!msg || msg.type !== "SCENE_UPDATE") return;
      const elements = msg.payload?.elements;
      if (!Array.isArray(elements) || elements.length === 0) return;
      const pending = persister.append(roomId, elements);
      if (!pending) return;
      void pending.then(
        (seq) => {
          socket.to(roomId).emit("client-broadcast", encodeBroadcastPayload({ ...msg, seq }));
          if (typeof ack === "function") {
            ack({ seq });
          }
        },
        () => {
          if (typeof ack === "function") {
            ack({ error: "persist_failed" });
          }
        },
      );
    });

    // sync-request(协议 v2,Sync Step 1/2):客户端带游标请求状态补丁,
    // room 回调 Go API scene-diff 计算差异(无状态纯转发)。任意已入房
    // 角色可用(viewer 也可,只读路径)。
    socket.on("sync-request", async (roomId: unknown, payload: unknown, ack?: (result: unknown) => void) => {
      if (typeof roomId !== "string" || typeof ack !== "function") return;
      if (!socket.rooms.has(roomId)) {
        ack({ error: "forbidden" });
        return;
      }
      const afterSeq = parseAfterSeq(payload);
      const patch = await fetchSceneDiff(config, roomId, afterSeq, syncDiffLimit);
      if (!patch) {
        ack({ error: "sync_failed" });
        return;
      }
      ack(patch);
    });

    socket.on("server-volatile-broadcast", (roomId: unknown, data?: unknown) => {
      if (typeof roomId !== "string") return;
      // 允许:已加入的房间,或自己专属的 follow@<socketId> 动态房间
      // (滚动跟随:被跟随者不在该房间内,由 followers 加入)。
      const allowed =
        socket.rooms.has(roomId) ||
        (roomId.startsWith(followRoomPrefix) && roomId === followRoomPrefix + socket.id);
      if (!allowed || !rateLimit.allow(socket.id)) return;
      socket.volatile.to(roomId).emit("client-broadcast", data);
    });

    socket.on("user-follow", (payload?: unknown) => {
      const parsed = parseFollowPayload(payload);
      if (!parsed) return;
      const target = followRoomPrefix + parsed.userToFollowSocketId;
      if (parsed.action === "FOLLOW") {
        socket.join(target);
      } else {
        socket.leave(target);
      }
      void updateFollowers(io, parsed.userToFollowSocketId);
    });

    socket.on("disconnecting", () => {
      for (const room of socket.rooms) {
        if (room === socket.id) continue;
        void broadcastRoster(io, room).catch(() => undefined);
      }
    });
  });
}

function deny(socket: Socket, ack: ((result: unknown) => void) | undefined, reason: string): void {
  socket.emit("join-room-error", { reason });
  if (typeof ack === "function") {
    ack({ error: reason });
  }
}

function connectionIdentity(socket: Socket): string {
  const data = socket.data as SocketData;
  // 握手鉴权 fail-closed,userId 与 guest 必有其一。
  return data.userId ?? data.guest!.subject;
}

async function broadcastRoster(io: Server, roomId: string): Promise<void> {
  const sockets = await io.in(roomId).fetchSockets();
  io.to(roomId).emit("room-user-change", sockets.map((s) => s.id));
}

async function updateFollowers(io: Server, targetSocketId: string): Promise<void> {
  const sockets = await io.in(followRoomPrefix + targetSocketId).fetchSockets();
  io.to(targetSocketId).emit("user-follow-room-change", sockets.map((s) => s.id));
}

interface FollowPayload {
  userToFollowSocketId: string;
  action: "FOLLOW" | "UNFOLLOW";
}

function parseFollowPayload(payload: unknown): FollowPayload | null {
  if (!payload || typeof payload !== "object") return null;
  const p = payload as { userToFollow?: { socketId?: unknown }; action?: unknown };
  if (typeof p.userToFollow?.socketId !== "string" || !p.userToFollow.socketId) return null;
  if (p.action !== "FOLLOW" && p.action !== "UNFOLLOW") return null;
  return { userToFollowSocketId: p.userToFollow.socketId, action: p.action };
}
