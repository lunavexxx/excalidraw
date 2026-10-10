import { newElementWith } from "@excalidraw/excalidraw";
import { syncInvalidIndices } from "@excalidraw/element";
import { API } from "@excalidraw/excalidraw/tests/helpers/api";

import type { ExcalidrawImperativeAPI } from "@excalidraw/excalidraw/types";

import { WS_EVENTS } from "../app_constants";
import { appJotaiStore } from "../app-jotai";
import { canvasRealtimeStatusAtom } from "../canvas/atoms";

import Collab, { isCollaboratingAtom } from "./Collab";

import type { Socket } from "socket.io-client";

const socketFixture = () => {
  const listeners = new Map<string, (...args: any[]) => void>();
  let role = "editor";
  let joinError: Error | null = null;
  const socket = {
    connected: true,
    on: vi.fn((event, handler) => {
      listeners.set(event, handler);
    }),
    emit: vi.fn((_event: string, ..._args: any[]) => {}),
    timeout: () => ({
      emit: (event: string, ...args: any[]) => {
        socket.emit(event, ...args);
        if (event === "join-room") {
          args.at(-1)(joinError, joinError ? undefined : { role });
        } else if (event === "sync-request") {
          args.at(-1)(null, { elements: [], cursor: 0, has_more: false });
        }
      },
    }),
    removeAllListeners: vi.fn(() => listeners.clear()),
    close: vi.fn(() => listeners.get("disconnect")?.("io client disconnect")),
    connect: vi.fn(),
    disconnect: vi.fn(() => {
      listeners.get("disconnect")?.("io client disconnect");
      return socket;
    }),
  };
  return {
    socket,
    fire: (event: string, ...args: any[]) => listeners.get(event)?.(...args),
    setRole: (r: string) => {
      role = r;
    },
    failJoin: () => {
      joinError = new Error("ack timeout");
    },
  };
};

it("retains disconnected edits until an authorized rejoin and then sends them", async () => {
  let elements = syncInvalidIndices([
    API.createElement({ type: "rectangle", id: "offline-edit" }),
  ]);
  const state = { viewModeEnabled: false };
  const api = {
    getAppState: () => state,
    getSceneElementsIncludingDeleted: () => elements,
    getFiles: () => ({}),
    updateScene: vi.fn(),
  } as unknown as ExcalidrawImperativeAPI;
  const collab = new Collab({ excalidrawAPI: api });
  vi.spyOn(collab, "setCollaborators").mockImplementation(() => {});
  vi.spyOn(collab, "onAccessChanged").mockImplementation(async (access) => {
    state.viewModeEnabled = access.role === "viewer";
  });
  const { socket, fire, setRole } = socketFixture();
  collab.portal.open(socket as unknown as Socket, "canvas");
  collab.portal.roomJoined = true;
  collab.portal.socketInitialized = true;
  collab.syncElements(elements);
  socket.emit.mockClear();
  socket.connected = false;
  fire("disconnect", "transport close");
  elements = [newElementWith(elements[0], { x: 42 })];
  collab.syncElements(elements);
  expect(socket.emit).not.toHaveBeenCalled();
  expect(collab.portal.broadcastedElementVersions.size).toBe(0);
  expect(appJotaiStore.get(canvasRealtimeStatusAtom)).toBe("reconnecting");
  socket.connected = true;
  fire("connect");
  collab.syncElements(elements);
  expect(socket.emit).not.toHaveBeenCalled();
  fire("init-room");
  await vi.waitFor(() =>
    expect(
      socket.emit.mock.calls.some((call) => call[0] === WS_EVENTS.SERVER),
    ).toBe(true),
  );
  const frame = socket.emit.mock.calls.find(
    (call) => call[0] === WS_EVENTS.SERVER,
  )!;
  expect(
    JSON.parse(new TextDecoder().decode(frame[2])).payload.elements[0].x,
  ).toBe(42);

  // A role downgrade during the outage prevents resend on rejoin.
  socket.emit.mockClear();
  socket.connected = false;
  fire("disconnect", "transport close");
  elements = [newElementWith(elements[0], { x: 99 })];
  setRole("viewer");
  socket.connected = true;
  fire("connect");
  fire("init-room");
  await Promise.resolve();
  await Promise.resolve();
  expect(
    socket.emit.mock.calls.some((call) => call[0] === WS_EVENTS.SERVER),
  ).toBe(false);
  collab.portal.queueFileUpload.cancel();
});
it("closing a canvas removes disconnect listeners so it cannot reconnect itself", () => {
  const collab = new Collab({ excalidrawAPI: {} as ExcalidrawImperativeAPI });
  const { socket } = socketFixture();
  collab.portal.open(socket as unknown as Socket, "canvas");
  collab.portal.close();
  expect(socket.removeAllListeners.mock.invocationCallOrder[0]).toBeLessThan(
    socket.close.mock.invocationCallOrder[0],
  );
  expect(socket.connect).not.toHaveBeenCalled();
  expect(collab.portal.isOpen()).toBe(false);
  appJotaiStore.set(isCollaboratingAtom, false);
});

it("restarts the handshake when a connected socket receives no join acknowledgement", () => {
  const collab = new Collab({ excalidrawAPI: {} as ExcalidrawImperativeAPI });
  vi.spyOn(collab, "setCollaborators").mockImplementation(() => {});
  const joined = vi.spyOn(collab, "onRoomJoined");
  const { socket, fire, failJoin } = socketFixture();
  collab.portal.open(socket as unknown as Socket, "canvas");
  failJoin();
  fire("init-room");
  expect(socket.disconnect).toHaveBeenCalledTimes(1);
  expect(socket.connect).toHaveBeenCalledTimes(1);
  expect(joined).not.toHaveBeenCalled();
  expect(collab.portal.isOpen()).toBe(false);
  collab.portal.close();
});

it("retries unacknowledged edits after patrol reconciliation updates the scene guard", async () => {
  const elements = syncInvalidIndices([
    API.createElement({ type: "rectangle", id: "retry-edit" }),
  ]);
  const api = {
    getAppState: () => ({ viewModeEnabled: false }),
    getSceneElementsIncludingDeleted: () => elements,
    getFiles: () => ({}),
    updateScene: vi.fn(),
  } as unknown as ExcalidrawImperativeAPI;
  const collab = new Collab({ excalidrawAPI: api });
  const { socket } = socketFixture();
  collab.portal.open(socket as unknown as Socket, "canvas");
  collab.portal.roomJoined = true;
  collab.portal.socketInitialized = true;
  appJotaiStore.set(isCollaboratingAtom, true);
  collab.syncElements(elements);
  const frame = socket.emit.mock.calls.find(
    (call) => call[0] === WS_EVENTS.SERVER,
  )!;
  frame.at(-1)(new Error("ack timeout"));
  expect(collab.portal.broadcastedElementVersions.size).toBe(0);

  // A patch can update the scene guard without confirming the pending send.
  const patrol = collab as unknown as {
    syncFromServerSeq: () => Promise<boolean>;
    patrolSeq: () => void;
  };
  vi.spyOn(patrol, "syncFromServerSeq").mockImplementation(async () => {
    collab.setLastBroadcastedOrReceivedSceneVersion(elements[0].version);
    return true;
  });
  socket.emit.mockClear();
  patrol.patrolSeq();
  await vi.waitFor(() =>
    expect(
      socket.emit.mock.calls.some((call) => call[0] === WS_EVENTS.SERVER),
    ).toBe(true),
  );
  expect(socket.emit).toHaveBeenCalledWith("presence-request", "canvas");
  collab.portal.queueFileUpload.cancel();
  collab.portal.close();
  appJotaiStore.set(isCollaboratingAtom, false);
});
