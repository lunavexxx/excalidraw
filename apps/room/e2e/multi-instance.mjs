// 双实例跨广播冒烟:两个 room 进程共享 redis(redis adapter),
// A 连实例 1、B 连实例 2 同房间,A 广播 → B 必须收到(跨实例 relay),
// B 侧 join 后 A 侧也必须收到跨实例 roster。
// 运行:node apps/room/e2e/multi-instance.mjs [token] [canvasId]
//   缺省时经 API_BASE 登录 + 建画布(TEST_PHONE/TEST_PASSWORD 环境)。
// 实例地址:ROOM1_URL(默认 127.0.0.1:3002)/ ROOM2_URL(默认 127.0.0.1:3003)
import { createRequire } from "node:module";

const require = createRequire(new URL("../../web/package.json", import.meta.url));
const { io } = require("socket.io-client");

const ROOM1 = process.env.ROOM1_URL || "http://127.0.0.1:3002";
const ROOM2 = process.env.ROOM2_URL || "http://127.0.0.1:3003";
const API_BASE = process.env.API_BASE || "";

async function prepare() {
  if (process.argv[2] && process.argv[3]) {
    return { token: process.argv[2], canvasId: process.argv[3] };
  }
  if (!API_BASE) throw new Error("need API_BASE or token+canvasId args");
  const login = await fetch(`${API_BASE}/auth/login`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ phone: process.env.TEST_PHONE, password: process.env.TEST_PASSWORD }),
  }).then((r) => r.json());
  if (login.code !== 0) throw new Error("login failed");
  const token = login.data.access_token;
  const created = await fetch(`${API_BASE}/canvases`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
    body: JSON.stringify({ name: `room-x2-${Date.now()}` }),
  }).then((r) => r.json());
  if (created.code !== 0) throw new Error("canvas create failed");
  return { token, canvasId: created.data.canvas.id };
}

const connect = (base, token) =>
  new Promise((resolve, reject) => {
    const s = io(base, { transports: ["websocket"], auth: { token }, reconnection: false });
    s.on("connect", () => resolve(s));
    s.on("connect_error", (e) => reject(e));
  });

const once = (s, ev, ms = 5000) =>
  new Promise((res, rej) => {
    const t = setTimeout(() => rej(new Error(`timeout ${ev}`)), ms);
    s.once(ev, (...a) => { clearTimeout(t); res(a); });
  });

const emitAck = (s, ev, ...args) => new Promise((r) => s.emit(ev, ...args, (x) => r(x)));

const main = async () => {
  const { token: TOKEN, canvasId: CANVAS } = await prepare();
  const a = await connect(ROOM1, TOKEN);
  const b = await connect(ROOM2, TOKEN);
  console.log(`A→${ROOM1} sid=${a.id.slice(0, 6)}  B→${ROOM2} sid=${b.id.slice(0, 6)}  canvas=${CANVAS.slice(0, 8)}`);

  // join 会无条件广播名册(含单人 [自己]);持久收集,等跨实例聚合出的 2 人名册。
  const rostersAtA = [];
  a.on("room-user-change", (ids) => rostersAtA.push(ids));
  const joinA = await emitAck(a, "join-room", CANVAS);
  if (!joinA?.role) throw new Error(`A join 失败: ${JSON.stringify(joinA)}`);
  const joinB = await emitAck(b, "join-room", CANVAS);
  if (!joinB?.role) throw new Error(`B join 失败: ${JSON.stringify(joinB)}`);
  const twoMemberRoster = await new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error(`超时未收到 2 人名册,收到: ${JSON.stringify(rostersAtA)}`)), 5000);
    const check = () => {
      const found = rostersAtA.find((ids) => Array.isArray(ids) && ids.length === 2);
      if (found) { clearTimeout(t); resolve(found); }
    };
    check();
    a.on("room-user-change", check);
  });
  console.log(`ok  跨实例 roster(A 收到 2 人: ${JSON.stringify(twoMemberRoster)})`);

  const bcAtB = once(b, "client-broadcast");
  const ack = await emitAck(a, "server-broadcast", CANVAS,
    new TextEncoder().encode(JSON.stringify({ type: "SCENE_UPDATE", payload: { elements: [{ id: "cross" }] } })));
  const bc = await bcAtB;
  const parsed = JSON.parse(new TextDecoder().decode(new Uint8Array(bc[0])));
  if (parsed.type !== "SCENE_UPDATE" || parsed.seq !== ack?.seq) {
    throw new Error(`跨实例广播失败: ack=${JSON.stringify(ack)} recv=${JSON.stringify(parsed).slice(0, 100)}`);
  }
  console.log(`ok  跨实例广播(A 广播 seq=${ack.seq} → B 收到)`);

  a.close();
  b.close();
  console.log("\n2/2 passed — redis adapter 跨实例工作正常");
  process.exit(0);
};

main().catch((err) => {
  console.error("FAIL:", err.message);
  process.exit(1);
});
