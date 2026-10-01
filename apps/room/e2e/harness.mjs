// e2e harness:用前端同款 socket.io-client 4.7.2 驱动 room 服务,覆盖
// 线协议全部事件语义。对真实服务时(登录 + 建画布 + 真落库)为验收级;
// 也可指向 spike 桩服务做纯协议验证。
// 运行:node apps/room/e2e/harness.mjs [roomBase]
// 环境变量:API_BASE / TEST_PHONE / TEST_PASSWORD / JWT_SECRET(smoke 启动脚本注入)
import { createRequire } from "node:module";
import crypto from "node:crypto";

const require = createRequire(new URL("../../web/package.json", import.meta.url));
const { io } = require("socket.io-client");

const BASE = process.argv[2] || "http://127.0.0.1:3002";
const API_BASE = process.env.API_BASE || "";
const TEST_PHONE = process.env.TEST_PHONE || "";
const TEST_PASSWORD = process.env.TEST_PASSWORD || "";
const JWT_SECRET = process.env.JWT_SECRET || "";

const UUID_A = "0b3f2a1c-1111-4222-8333-444455556666";

let passed = 0;
let failed = 0;
const failures = [];

function check(name, ok, detail = "") {
  if (ok) {
    passed++;
    console.log(`  ok  ${name}`);
  } else {
    failed++;
    failures.push(name);
    console.log(`FAIL  ${name}${detail ? ` — ${detail}` : ""}`);
  }
}

// ── 真实链路准备:登录 → 建画布(拿 owner 角色);无 API 环境则退化为纯协议模式 ──
async function apiCall(path, { method = "GET", body, token } = {}) {
  const resp = await fetch(`${API_BASE}${path}`, {
    method,
    headers: {
      "content-type": "application/json",
      ...(token ? { authorization: `Bearer ${token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  return resp.json();
}

async function prepare() {
  if (!API_BASE || !TEST_PHONE) return { mode: "protocol", canvasId: UUID_A, token: null };
  const login = await apiCall("/auth/login", {
    method: "POST",
    body: { phone: TEST_PHONE, password: TEST_PASSWORD },
  });
  if (login.code !== 0 || !login.data?.access_token) {
    throw new Error(`login failed: ${JSON.stringify(login).slice(0, 120)}`);
  }
  const token = login.data.access_token;
  const created = await apiCall("/canvases", {
    method: "POST",
    token,
    body: { name: `room-e2e-${Date.now()}` },
  });
  if (created.code !== 0 || !created.data?.canvas?.id) {
    throw new Error(`canvas create failed: ${JSON.stringify(created).slice(0, 120)}`);
  }
  return { mode: "full", canvasId: created.data.canvas.id, token };
}

// 手工签 HS256 JWT(仅测试用)
function signJWT(secret, claims) {
  const b64 = (obj) => Buffer.from(JSON.stringify(obj)).toString("base64url");
  const head = b64({ alg: "HS256", typ: "JWT" });
  const body = b64(claims);
  const sig = crypto.createHmac("sha256", secret).update(`${head}.${body}`).digest("base64url");
  return `${head}.${body}.${sig}`;
}

function connect(auth, setup) {
  return new Promise((resolve, reject) => {
    const socket = io(BASE, { transports: ["websocket"], auth, reconnection: false });
    if (setup) setup(socket);
    socket.on("connect", () => resolve(socket));
    socket.on("connect_error", (err) => reject(err));
  });
}

const once = (socket, event, timeoutMs = 5000) =>
  new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error(`timeout waiting "${event}"`)), timeoutMs);
    socket.once(event, (...args) => {
      clearTimeout(t);
      resolve(args);
    });
  });

const emitAck = (socket, event, ...args) =>
  new Promise((resolve) => socket.emit(event, ...args, (res) => resolve(res)));

// Node 运行时二进制附件是 Buffer,浏览器是 ArrayBuffer(线上前端形态),两者皆收。
const asBytes = (data) => {
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (Buffer.isBuffer(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  if (data instanceof Uint8Array) return data;
  return null;
};

const main = async () => {
  // 看门狗:任何未设超时的挂起点(连接/ack)60s 后强制失败退出。
  setTimeout(() => {
    console.error("\nharness watchdog: 60s exceeded — force exit");
    console.log(`\n${passed} passed, ${failed} failed${failed ? `: ${failures.join(", ")}` : ""}`);
    process.exit(2);
  }, 60_000);
  const { mode, canvasId, token } = await prepare();
  console.log(`# mode=${mode} canvas=${canvasId}`);

  // ── 1. 握手鉴权 accept/reject + connect_error 文案 + init-room ──
  let initedResolve;
  const inited = new Promise((r) => (initedResolve = r));
  const s1 = await connect({ token }, (socket) => socket.once("init-room", initedResolve));
  check("1a 握手有效 token 可连接", s1.connected);
  await inited;
  check("1b 连接后收到 init-room", true);
  let rejectMsg = "";
  await new Promise((resolve) => {
    const bad = io(BASE, { transports: ["websocket"], auth: { token: "" }, reconnection: false });
    bad.on("connect_error", (err) => {
      rejectMsg = err.message;
      bad.close();
      resolve();
    });
  });
  check("1c 空 token 被拒", rejectMsg === "invalid token", `message=${rejectMsg}`);
  const badToken = JWT_SECRET ? signJWT(JWT_SECRET, { typ: "access", sub: "no-such-user", exp: Math.floor(Date.now() / 1000) + 60 }) : "garbage";
  rejectMsg = "";
  await new Promise((resolve) => {
    const bad = io(BASE, { transports: ["websocket"], auth: { token: `${badToken}-x` }, reconnection: false });
    bad.on("connect_error", (err) => {
      rejectMsg = err.message;
      bad.close();
      resolve();
    });
  });
  check("1d 坏签名 token 被拒", rejectMsg === "invalid token", `message=${rejectMsg}`);

  // ── 2. join-room ack/拒绝路径 ──
  const joinAck = await emitAck(s1, "join-room", canvasId);
  check("2a join ack 回角色(owner)", typeof joinAck?.role === "string" && joinAck.role !== "", JSON.stringify(joinAck));
  const denyAck = await emitAck(s1, "join-room", "not-a-uuid");
  check("2b 非法房间 deny ack", denyAck && denyAck.error === "invalid_room", JSON.stringify(denyAck));
  const forbiddenAck = await emitAck(s1, "join-room", "0b3f2a1c-1111-4222-8333-444455559999");
  check("2c 无权限房间 fail-closed", forbiddenAck && forbiddenAck.error === "forbidden", JSON.stringify(forbiddenAck));

  // ── 3. 二进制帧往返 + persist-then-relay(seq 回执) ──
  const s2 = await connect({ token });
  const roster2 = once(s1, "room-user-change");
  const joinAck2 = await emitAck(s2, "join-room", canvasId);
  check("3a 双连接 join 均成功", !!joinAck2?.role, JSON.stringify(joinAck2));
  const roster2Res = await roster2;
  check("3b 双人 roster", Array.isArray(roster2Res[0]) && roster2Res[0].length === 2, JSON.stringify(roster2Res));

  const frame = { type: "SCENE_UPDATE", payload: { elements: [{ id: "e1", type: "rectangle", version: 1 }] } };
  const bcPromise = once(s2, "client-broadcast");
  const seqAck = await emitAck(
    s1,
    "server-broadcast",
    canvasId,
    new TextEncoder().encode(JSON.stringify(frame)),
  );
  check("3c 广播 ack 带真实落库 seq", typeof seqAck?.seq === "number" && seqAck.seq > 0, JSON.stringify(seqAck));
  const bc = await bcPromise;
  let parsed = null;
  try {
    parsed = JSON.parse(new TextDecoder().decode(asBytes(bc[0])));
  } catch {}
  check(
    "3d 对端收到二进制帧并解析(含 seq)",
    !!parsed && parsed.type === "SCENE_UPDATE" && parsed.seq === seqAck.seq && parsed.payload?.elements?.[0]?.id === "e1",
    `recv=${bc[0]?.constructor?.name}`,
  );

  // ── 4. 未入房间的广播被静默丢弃(无 ack、无转发) ──
  // viewer 路径需 guest token(分享链接签发),属双浏览器清单范围。
  const outsideBC = once(s2, "client-broadcast", 2000).catch(() => "dropped");
  const ackOutside = emitAck(s1, "server-broadcast", "0b3f2a1c-1111-4222-8333-444455559999", new TextEncoder().encode(JSON.stringify(frame)));
  const dropped = await Promise.race([
    ackOutside.then((res) => res === undefined || res == null ? "dropped" : `acked:${JSON.stringify(res)}`),
    new Promise((r) => setTimeout(() => r("dropped"), 2000)),
  ]);
  const relayed = await outsideBC;
  check("4a 未入房广播静默丢弃", dropped === "dropped" && relayed === "dropped", `ack=${dropped} relay=${JSON.stringify(relayed).slice(0, 80)}`);

  // ── 5. sync-request(scene-diff 真实回源) ──
  const sync = await emitAck(s2, "sync-request", canvasId, { after_seq: 0 });
  check(
    "5 sync-request 补丁(elements+cursor)",
    sync && !sync.error && Array.isArray(sync.elements) && typeof sync.cursor === "number" && sync.cursor >= seqAck.seq && sync.has_more === false,
    JSON.stringify(sync).slice(0, 160),
  );
  const syncForbidden = await emitAck(s2, "sync-request", "0b3f2a1c-1111-4222-8333-444455559999", { after_seq: 0 });
  check("5b 未入房 sync 拒绝", syncForbidden && syncForbidden.error === "forbidden", JSON.stringify(syncForbidden));

  // ── 6. volatile 透传(排除发送者) ──
  const presence = { type: "MOUSE_LOCATION", socketId: "sid-A", pointer: [1, 2] };
  const volatilePromise = once(s2, "client-broadcast");
  s1.volatile.emit("server-volatile-broadcast", canvasId, new TextEncoder().encode(JSON.stringify(presence)));
  const vbc = await volatilePromise;
  let vParsed = null;
  try {
    vParsed = JSON.parse(new TextDecoder().decode(asBytes(vbc[0])));
  } catch {}
  check("6 volatile 转发到他人", vParsed && vParsed.type === "MOUSE_LOCATION", `recv=${vbc[0]?.constructor?.name}`);

  // ── 7. user-follow 动态房间 ──
  const followChange = once(s1, "user-follow-room-change");
  s2.emit("user-follow", { userToFollow: { socketId: s1.id }, action: "FOLLOW" });
  const fc = await followChange;
  check("7a follow 后目标收到名册", Array.isArray(fc[0]) && fc[0].includes(s2.id), JSON.stringify(fc));
  const unfollowChange = once(s1, "user-follow-room-change");
  s2.emit("user-follow", { userToFollow: { socketId: s1.id }, action: "UNFOLLOW" });
  const fc2 = await unfollowChange;
  check("7b unfollow 后名册清空", Array.isArray(fc2[0]) && fc2[0].length === 0, JSON.stringify(fc2));

  // ── 8. 断开后的 roster(disconnecting 重播,不含幽灵) ──
  const rosterAfterLeave = once(s1, "room-user-change");
  s2.close();
  const roster3 = await rosterAfterLeave;
  check(
    "8 断开重播 roster 收缩(无幽灵)",
    Array.isArray(roster3[0]) && roster3[0].length === 1 && roster3[0][0] === s1.id,
    JSON.stringify(roster3),
  );

  // ── 9. 超限帧(7MB > 6MB)触发断连 ──
  const s3 = await connect({ token });
  await emitAck(s3, "join-room", canvasId);
  const disconnectPromise = new Promise((resolve) => s3.on("disconnect", (reason) => resolve(reason)));
  const big = new TextEncoder().encode(
    JSON.stringify({ type: "SCENE_UPDATE", payload: { elements: [{ blob: "x".repeat(7 * 1024 * 1024) }] } }),
  );
  s3.emit("server-broadcast", canvasId, big, () => {});
  const reason = await disconnectPromise;
  check("9 超限帧断连", typeof reason === "string", `reason=${reason}`);
  s3.close();

  s1.close();
  console.log(`\n${passed} passed, ${failed} failed${failed ? `: ${failures.join(", ")}` : ""}`);
  process.exit(failed ? 1 : 0);
};

main().catch((err) => {
  console.error("harness fatal:", err);
  process.exit(1);
});
