// Run against an isolated test stack: API_BASE, ROOM_A and ROOM_B are required.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(
  new URL("../../web/package.json", import.meta.url)
);
const { io } = require("socket.io-client");
const API = process.env.API_BASE;
const ROOM_A = process.env.ROOM_A;
const ROOM_B = process.env.ROOM_B;
assert(
  API && ROOM_A && ROOM_B,
  "Set API_BASE, ROOM_A and ROOM_B to an isolated test stack"
);
const connections = [];
const password = "collaboration-test-only";
const seed = String(Date.now()).slice(-7);
async function api(path, token, method = "GET", body, entry) {
  const response = await fetch(`${API}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(entry ? { "X-Access-Entry": entry } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const result = await response.json();
  assert.equal(result.code, 0, `${method} ${path}: ${result.message}`);
  return result.data;
}
function wait(socket, event, predicate = () => true, timeout = 8000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      socket.off(event, handler);
      reject(new Error(`Timed out: ${event}`));
    }, timeout);
    const handler = (data) => {
      if (predicate(data)) {
        clearTimeout(timer);
        socket.off(event, handler);
        resolve(data);
      }
    };
    socket.on(event, handler);
  });
}
async function connect(base, token, canvasId) {
  const s = io(base, {
    transports: ["websocket"],
    auth: { token },
    reconnection: false,
    autoConnect: false,
  });
  connections.push(s);
  const ready = new Promise((resolve, reject) => {
    s.once("connect_error", reject);
    s.once("init-room", () => {
      if (canvasId) {
        s.emit("join-room", canvasId, (result) =>
          result?.error ? reject(new Error(result.error)) : resolve(result)
        );
      } else {
        resolve({});
      }
    });
  });
  s.connect();
  await ready;
  return s;
}
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
try {
  const users = [];
  for (let i = 0; i < 4; i++) {
    users.push(
      await api("/auth/register", null, "POST", {
        phone: `138${seed}${i}`,
        password,
        nickname: [
          "AccessOwner",
          "AccessEditor",
          "AccessViewer",
          "AccessApplicant",
        ][i],
      })
    );
  }
  const [owner, editor, viewer, applicant] = users;
  const token = owner.access_token;
  const { canvas } = await api("/canvases", token, "POST", {
    name: "Collaboration access e2e",
  });
  const path = `/canvases/${canvas.id}`;
  await api(`${path}/collaborators`, token, "PUT", {
    phone: `138${seed}1`,
    role: "editor",
  });
  await api(`${path}/collaborators`, token, "PUT", {
    phone: `138${seed}2`,
    role: "viewer",
  });
  const a = await connect(ROOM_A, token, canvas.id);
  const b = await connect(ROOM_B, editor.access_token, canvas.id);
  const c = await connect(ROOM_B, viewer.access_token, canvas.id);
  const rosterPromise = wait(a, "presence-roster", (r) => r.length === 3);
  a.emit("presence-request", canvas.id);
  const roster = await rosterPromise;
  assert.deepEqual(
    new Set(roster.map((r) => r.user_id)),
    new Set([owner.user.id, editor.user.id, viewer.user.id])
  );
  assert(roster.every((r) => r.nickname.startsWith("Access") && !r.token));
  console.log("PASS cross-instance trusted presence");
  const frame = (n) =>
    Buffer.from(
      JSON.stringify({
        type: "SCENE_UPDATE",
        payload: {
          elements: [
            {
              id: `element-${n}`,
              type: "rectangle",
              version: 1,
              versionNonce: n,
              x: 0,
              y: 0,
              width: 10,
              height: 10,
              isDeleted: false,
            },
          ],
        },
      })
    );
  const relayed = wait(
    a,
    "client-broadcast",
    (raw) => JSON.parse(Buffer.from(raw)).type === "SCENE_UPDATE"
  );
  b.emit("server-broadcast", canvas.id, frame(1));
  await relayed;
  console.log("PASS editor updates persist and relay");
  const downgraded = wait(
    b,
    "canvas-access-changed",
    (data) => data.role === "viewer"
  );
  await api(`${path}/collaborators/${editor.user.id}`, token, "PATCH", {
    role: "viewer",
  });
  await downgraded;
  let unauthorized = 0;
  const watch = () => {
    unauthorized++;
  };
  a.on("client-broadcast", watch);
  b.emit("server-broadcast", canvas.id, frame(2));
  c.emit("server-broadcast", canvas.id, frame(3));
  await pause(300);
  a.off("client-broadcast", watch);
  assert.equal(unauthorized, 0);
  console.log("PASS connected editor downgrade blocks writes across instances");
  const removed = wait(c, "canvas-access-changed", (data) => data.role === "");
  await api(`${path}/collaborators/${viewer.user.id}`, token, "DELETE");
  await removed;
  console.log("PASS connected member removal");
  const personal = await connect(ROOM_B, applicant.access_token);
  const entry = await api(`${path}/access-entries`, token, "POST");
  const submitted = await api(
    `${path}/access-requests`,
    applicant.access_token,
    "POST",
    { role: "editor", reason: "Join" },
    entry.token
  );
  const duplicate = await api(
    `${path}/access-requests`,
    applicant.access_token,
    "POST",
    { role: "editor" },
    entry.token
  );
  assert.equal(duplicate.id, submitted.id);
  const notice = wait(personal, "notifications-changed");
  await api(`${path}/access-requests/${submitted.id}/decision`, token, "POST", {
    decision: "approved",
    role: "editor",
  });
  await notice;
  const detail = await api(path, applicant.access_token);
  assert.equal(detail.my_role, "editor");
  const messages = await api("/notifications", applicant.access_token);
  assert(messages.items.some((n) => n.type === "access_approved"));
  console.log(
    "PASS request approval and personal notification without canvas access"
  );
  const invitation = await api(`${path}/invitations`, token, "POST", {
    phone: `138${seed}4`,
    role: "viewer",
  });
  const invitee = await api("/auth/register", null, "POST", {
    phone: `138${seed}4`,
    password,
    nickname: "AccessInvitee",
  });
  const accepted = await api(
    "/invitations/accept",
    invitee.access_token,
    "POST",
    { token: invitation.token }
  );
  assert.equal(accepted.canvas_id, canvas.id);
  await api("/invitations/accept", invitee.access_token, "POST", {
    token: invitation.token,
  });
  console.log("PASS invitation before registration and idempotent acceptance");
  const link = await api(`${path}/share-links`, token, "POST", {
    role: "editor",
    expires_in_days: 1,
  });
  const guest = await api(`${path}/access`, null, "POST", {
    share_token: link.share_link.token,
  });
  const g = await connect(ROOM_B, guest.access_token, canvas.id);
  const revoked = wait(g, "canvas-access-changed", (data) => data.role === "");
  await api(`${path}/share-links/${link.share_link.id}`, token, "DELETE");
  await revoked;
  console.log("PASS active guest share-link revocation");
} finally {
  connections.forEach((s) => s.close());
}
