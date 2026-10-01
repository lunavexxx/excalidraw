// server.ts:装配 http + socket.io(redis adapter)+ 鉴权 + 协议 + 健康检查。
import { createServer } from "node:http";
import { Server } from "socket.io";
import { createAdapter } from "@socket.io/redis-adapter";
import { Redis } from "ioredis";

import { loadConfig } from "./config";
import { authenticate, type SocketData } from "./auth";
import { registerRoomHandlers } from "./rooms";
import { EventPersister } from "./persist";
import { ConnectionGuard, createRateLimiter } from "./guard";

async function main(): Promise<void> {
  const config = loadConfig();

  const httpServer = createServer((req, res) => {
    if (req.url === "/healthz") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ status: "ok" }));
      return;
    }
    res.writeHead(404);
    res.end();
  });

  const io = new Server(httpServer, {
    cors: config.corsOrigins.length ? { origin: config.corsOrigins } : undefined,
    maxHttpBufferSize: config.maxHttpBufferSize,
    // 生产经 Caddy 反代 websocket-only(免粘性会话);dev CORS_ORIGINS
    // 未配置时由 engine.io 自行协商。
  });

  const pubClient = new Redis(config.redisUrl, { lazyConnect: false, maxRetriesPerRequest: null });
  const subClient = pubClient.duplicate();
  await Promise.all([pubClient.connect(), subClient.connect()]);
  io.adapter(createAdapter(pubClient, subClient));

  // 握手鉴权(fail-closed):无效 token 拒绝连接。
  io.use((socket, next) => {
    const token = socket.handshake.auth?.token;
    const identity = typeof token === "string" ? authenticate(config, token) : null;
    if (!identity) {
      next(new Error("invalid token"));
      return;
    }
    socket.data = { ...identity } as SocketData;
    next();
  });

  const persister = new EventPersister(config.databaseUrl, config.persistQueueMax);
  const rateLimit = createRateLimiter(config.rateLimitPerSecond);
  const connections = new ConnectionGuard(config.perIdentityConnectionLimit);
  registerRoomHandlers(io, config, persister, rateLimit, connections);

  const shutdown = async (signal: string) => {
    console.log(`[room] ${signal}, shutting down`);
    // 优雅退出:停收新连接 → 冲洗落库队列 → 关闭资源。
    io.close();
    await persister.close();
    pubClient.disconnect();
    subClient.disconnect();
    process.exit(0);
  };
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));

  await new Promise<void>((resolve) => httpServer.listen(config.port, resolve));
  console.log(`[room] listening on :${config.port} (redis adapter ready)`);
}

main().catch((err) => {
  console.error("[room] fatal:", err);
  process.exit(1);
});
