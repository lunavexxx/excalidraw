// Run with API_BASE, ROOM_A, ROOM_B pointed at an isolated migrated stack.
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { createRequire } from "node:module";

const require = createRequire(
  new URL("../../web/package.json", import.meta.url)
);
const { io } = require("socket.io-client");
const { API_BASE, ROOM_A, ROOM_B } = process.env;
assert(
  API_BASE && ROOM_A && ROOM_B,
  "An isolated API and two room instances are required"
);
const connections = [];
const watchdog = setTimeout(() => {
  console.error("Comment e2e timed out");
  process.exit(2);
}, 60000);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function request(path, token, method = "GET", body, code = 0) {
  const response = await fetch(`${API_BASE}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const result = await response.json();
  assert.equal(result.code, code, `${method} ${path}: ${result.message}`);
  return result.data;
}
function wait(socket, event, match = () => true) {
  return new Promise((resolve, reject) => {
    const handler = (data) => {
      if (match(data)) {
        clearTimeout(timer);
        socket.off(event, handler);
        resolve(data);
      }
    };
    const timer = setTimeout(() => {
      socket.off(event, handler);
      reject(new Error(`Timeout: ${event}`));
    }, 8000);
    socket.on(event, handler);
  });
}
async function connect(base, token, canvasId) {
  const socket = io(base, {
    transports: ["websocket"],
    auth: { token },
    reconnection: false,
    autoConnect: false,
  });
  connections.push(socket);
  const ready = new Promise((resolve, reject) => {
    socket.once("connect_error", reject);
    socket.once("init-room", () =>
      canvasId
        ? socket
            .timeout(6000)
            .emit("join-room", canvasId, (err, result) =>
              err || result?.error
                ? reject(err || new Error(result.error))
                : resolve(result)
            )
        : resolve({})
    );
  });
  socket.connect();
  await ready;
  return socket;
}
try {
  const seed = String(Date.now()).slice(-7);
  const owner = await request("/auth/register", null, "POST", {
    phone: `139${seed}0`,
    password: "comments-test-only",
    nickname: "CommentOwner",
  });
  const viewer = await request("/auth/register", null, "POST", {
    phone: `139${seed}1`,
    password: "comments-test-only",
    nickname: "CommentViewer",
  });
  const { canvas } = await request("/canvases", owner.access_token, "POST", {
    name: "Comments multi-instance e2e",
  });
  const base = `/canvases/${canvas.id}`;
  await request(`${base}/collaborators`, owner.access_token, "PUT", {
    phone: `139${seed}1`,
    role: "viewer",
  });
  const a = await connect(ROOM_A, owner.access_token, canvas.id);
  const b = await connect(ROOM_B, viewer.access_token, canvas.id);
  const personal = await connect(ROOM_A, viewer.access_token);
  const link = await request(
    `${base}/share-links`,
    owner.access_token,
    "POST",
    { role: "editor", expires_in_days: 1 }
  );
  const guest = await request(`${base}/access`, null, "POST", {
    share_token: link.share_link.token,
  });
  const g = await connect(ROOM_B, guest.access_token, canvas.id);
  const updatedA = wait(a, "comments-changed");
  const updatedB = wait(b, "comments-changed");
  const updatedGuest = wait(g, "comments-changed");
  const notice = wait(personal, "notifications-changed");
  const input = {
    body: "👨‍👩‍👧‍👦 @CommentViewer 🇨🇳",
    mentions: [{ user_id: viewer.user.id, start: 12, end: 26 }],
    request_id: randomUUID(),
    anchor: { x: 120, y: 160 },
  };
  const created = await request(
    `${base}/comment-threads`,
    owner.access_token,
    "POST",
    input
  );
  const threadPath = `${base}/comment-threads/${created.thread_id}`;
  const events = await Promise.all([updatedA, updatedB, updatedGuest, notice]);
  assert(
    events
      .slice(0, 3)
      .every(
        (e) => e.thread_id === created.thread_id && e.version === 1 && !e.body
      )
  );
  const retry = await request(
    `${base}/comment-threads`,
    owner.access_token,
    "POST",
    input
  );
  assert.equal(retry.thread_id, created.thread_id);
  const notices = await request("/notifications", viewer.access_token);
  assert.equal(
    notices.items.filter((n) => n.entity_id === created.thread_id).length,
    1
  );
  const seen = await request(threadPath, guest.access_token);
  assert.equal(seen.comments[0].body, input.body);
  await request(
    `${threadPath}/comments`,
    guest.access_token,
    "POST",
    { body: "guest", request_id: randomUUID() },
    42002
  );
  console.log(
    "PASS cross-instance owner/viewer/guest hints, mentions, personal notification and retry dedupe"
  );

  const replyA = wait(a, "comments-changed", (e) => e.version > 1);
  await request(`${threadPath}/comments`, viewer.access_token, "POST", {
    body: "👍🏽 viewer reply",
    request_id: randomUUID(),
  });
  await replyA;
  const ownerDetail = await request(threadPath, owner.access_token);
  assert.equal(ownerDetail.comments.length, 2);
  assert(ownerDetail.thread.is_unread);
  const oldUnread = ownerDetail.thread.unread_version;
  const rootId = ownerDetail.comments[0].id;
  await Promise.all(
    [owner, viewer].map((u) =>
      request(
        `${threadPath}/comments/${rootId}/reactions`,
        u.access_token,
        "PUT",
        { emoji: "👍🏽" }
      )
    )
  );
  await request(
    `${threadPath}/comments/${rootId}/reactions`,
    viewer.access_token,
    "PUT",
    { emoji: "👍🏽" }
  );
  const reacted = await request(threadPath, owner.access_token);
  assert.equal(reacted.comments[0].reactions[0].users.length, 2);
  assert.equal(reacted.thread.unread_version, oldUnread);
  await request(`${threadPath}/comments`, owner.access_token, "POST", {
    body: "new unread reply",
    request_id: randomUUID(),
  });
  const readTarget = await request(threadPath, viewer.access_token);
  const readHint = wait(
    personal,
    "comment-reads-changed",
    (e) => e.canvas_id === canvas.id
  );
  await request(`${threadPath}/read`, viewer.access_token, "POST", {
    version: readTarget.thread.version,
    unread_version: readTarget.thread.unread_version,
  });
  await readHint;
  assert(
    !(await request("/notifications", viewer.access_token)).items.some(
      (n) => n.entity_id === created.thread_id && !n.read_at
    )
  );
  console.log(
    "PASS viewer replies, concurrent idempotent reactions, independent unread version and personal read synchronization"
  );

  const resolution = wait(
    b,
    "comments-changed",
    (e) => e.version > reacted.thread.version
  );
  await request(threadPath, owner.access_token, "PATCH", {
    version: readTarget.thread.version,
    resolved: true,
  });
  await resolution;
  await request(
    `${threadPath}/comments`,
    viewer.access_token,
    "POST",
    { body: "late reply", request_id: randomUUID() },
    43002
  );
  const resolved = await request(threadPath, owner.access_token);
  await request(threadPath, owner.access_token, "PATCH", {
    version: resolved.thread.version,
    resolved: false,
  });
  b.close();
  await request(`${threadPath}/comments`, owner.access_token, "POST", {
    body: "offline recovery 👀",
    request_id: randomUUID(),
  });
  const reconnected = await connect(ROOM_B, viewer.access_token, canvas.id);
  const restored = await request(threadPath, viewer.access_token);
  assert(restored.comments.some((c) => c.body === "offline recovery 👀"));
  assert(restored.thread.is_unread);
  console.log(
    "PASS resolve/reopen ordering and reconnect recovery from committed API state"
  );

  const revoked = wait(
    reconnected,
    "canvas-access-changed",
    (e) => e.role === ""
  );
  await request(
    `${base}/collaborators/${viewer.user.id}`,
    owner.access_token,
    "DELETE"
  );
  await revoked;
  await request(threadPath, viewer.access_token, "GET", undefined, 41001);
  let leaked = 0;
  reconnected.on("comments-changed", () => leaked++);
  await request(`${threadPath}/comments`, owner.access_token, "POST", {
    body: "after removal",
    request_id: randomUUID(),
  });
  await pause(1300);
  assert.equal(leaked, 0);
  console.log(
    "PASS access removal blocks comment reads and subsequent room hints"
  );
} finally {
  clearTimeout(watchdog);
  connections.forEach((socket) => socket.close());
}
