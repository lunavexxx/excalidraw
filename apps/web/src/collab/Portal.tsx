import { CaptureUpdateAction } from "@excalidraw/excalidraw";
import { trackEvent } from "@excalidraw/excalidraw/analytics";
import { newElementWith } from "@excalidraw/element";
import throttle from "lodash.throttle";

import type { UserIdleState } from "@excalidraw/common";
import type { OrderedExcalidrawElement } from "@excalidraw/element/types";
import type {
  OnUserFollowedPayload,
  SocketId,
} from "@excalidraw/excalidraw/types";

import { WS_EVENTS, FILE_UPLOAD_TIMEOUT, WS_SUBTYPES } from "../app_constants";
import { isSyncableElement } from "../data";

import { appJotaiStore } from "../app-jotai";
import {
  presenceConnectedAtom,
  onlineUsersAtom,
  collaborationRefreshAtom,
} from "../canvas/atoms";

import type {
  SocketUpdateData,
  SocketUpdateDataSource,
  SyncableExcalidrawElement,
} from "../data";
import type { TCollabClass } from "./Collab";
import type { Socket } from "socket.io-client";

class Portal {
  collab: TCollabClass;
  socket: Socket | null = null;
  socketInitialized: boolean = false; // we don't want the socket to emit any updates until it is fully initialized
  roomId: string | null = null;
  broadcastedElementVersions: Map<string, number> = new Map();

  constructor(collab: TCollabClass) {
    this.collab = collab;
  }

  open(socket: Socket, id: string) {
    this.socket = socket;
    this.roomId = id;

    // Initialize socket listeners
    this.socket.on("init-room", () => {
      if (this.socket) {
        // join-room ack 触发游标同步(协议 v2);旧 room 无 ack,由
        // first-in-room / 超时兜底计时器触发,行为等价。
        this.socket.emit(
          "join-room",
          this.roomId,
          (res?: { error?: string; role?: string } | null) => {
            if (this.socket && !res?.error) {
              this.collab.onRoomJoined();
              if (res?.role && this.roomId) {
                void this.collab.onAccessChanged({
                  canvas_id: this.roomId,
                  role: res.role,
                });
              }
              appJotaiStore.set(presenceConnectedAtom, true);
              this.socket.emit("presence-request", this.roomId);
            }
          },
        );
        trackEvent("share", "room joined");
      }
    });
    this.socket.on(
      "presence-roster",
      (
        people: {
          socket_id: SocketId;
          user_id: string;
          nickname: string;
          avatar_url: string;
          role: string;
        }[],
      ) => {
        this.collab.setPresence(people);
      },
    );
    this.socket.on(
      "canvas-access-changed",
      (access: { canvas_id: string; role: string; unavailable?: boolean }) => {
        void this.collab.onAccessChanged(access);
      },
    );
    this.socket.on("canvas-members-changed", () => {
      appJotaiStore.set(collaborationRefreshAtom, (n) => n + 1);
    });
    this.socket.on("notifications-changed", () => {
      appJotaiStore.set(collaborationRefreshAtom, (n) => n + 1);
    });
    this.socket.on("disconnect", (reason: string) => {
      appJotaiStore.set(presenceConnectedAtom, false);
      appJotaiStore.set(onlineUsersAtom, new Map());
      if (reason === "io server disconnect") {
        this.socket?.connect();
      }
    });
    this.socket.on("room-user-change", (clients: SocketId[]) => {
      this.collab.setCollaborators(clients);
    });
    // 重连补拉:socket.io 自动重连后 init-room → join-room 会重放,
    // 但 first-in-room 是 .once、SCENE_INIT 消费 guard 也已失效;
    // 断线窗口的场景变化由服务端持久层补齐(绝不盲覆盖)。
    this.socket.on("connect", () => {
      if (this.roomId) {
        this.collab.onSocketReconnected();
      }
    });

    return socket;
  }

  close() {
    if (!this.socket) {
      return;
    }
    this.queueFileUpload.flush();
    this.socket.close();
    appJotaiStore.set(presenceConnectedAtom, false);
    appJotaiStore.set(onlineUsersAtom, new Map());
    this.socket = null;
    this.roomId = null;
    this.socketInitialized = false;
    this.broadcastedElementVersions = new Map();
  }

  isOpen() {
    return !!(this.socketInitialized && this.socket && this.roomId);
  }

  async _broadcastSocketData(
    data: SocketUpdateData,
    volatile: boolean = false,
    roomId?: string,
  ) {
    if (this.isOpen()) {
      if (
        !volatile &&
        this.collab.excalidrawAPI.getAppState().viewModeEnabled
      ) {
        return;
      }
      // 明文 JSON 广播(传输由 TLS 保护;房间内容由 ACL 把关)。
      const json = JSON.stringify(data);
      const encoded = new TextEncoder().encode(json);
      const target = roomId ?? this.roomId;

      if (volatile) {
        this.socket?.emit(WS_EVENTS.SERVER_VOLATILE, target, encoded);
        return;
      }
      // 非 volatile 帧要求落库确认(协议 v2):ack 携带 seq 表示已
      // 持久化并转发;persist_failed 表示该帧既未落库也未转发。
      this.socket?.emit(
        WS_EVENTS.SERVER,
        target,
        encoded,
        (res?: { seq?: number; error?: string } | null) => {
          if (!this.roomId) {
            return;
          }
          if (typeof res?.seq === "number") {
            this.collab.onOwnPersistedSeq(this.roomId, res.seq);
          } else if (res?.error) {
            this.collab.onOwnPersistFailed(this.roomId);
          }
        },
      );
    }
  }

  queueFileUpload = throttle(async () => {
    try {
      await this.collab.fileManager.saveFiles({
        elements: this.collab.excalidrawAPI.getSceneElementsIncludingDeleted(),
        files: this.collab.excalidrawAPI.getFiles(),
      });
    } catch (error: any) {
      if (error.name !== "AbortError") {
        this.collab.excalidrawAPI.updateScene({
          appState: {
            errorMessage: error.message,
          },
        });
      }
    }

    let isChanged = false;
    const newElements = this.collab.excalidrawAPI
      .getSceneElementsIncludingDeleted()
      .map((element) => {
        if (this.collab.fileManager.shouldUpdateImageElementStatus(element)) {
          isChanged = true;
          // this will signal collaborators to pull image data from server
          // (using mutation instead of newElementWith otherwise it'd break
          // in-progress dragging)
          return newElementWith(element, { status: "saved" });
        }
        return element;
      });

    if (isChanged) {
      this.collab.excalidrawAPI.updateScene({
        elements: newElements,
        captureUpdate: CaptureUpdateAction.NEVER,
      });
    }
  }, FILE_UPLOAD_TIMEOUT);

  broadcastScene = async (
    updateType: WS_SUBTYPES.INIT | WS_SUBTYPES.UPDATE,
    elements: readonly OrderedExcalidrawElement[],
    syncAll: boolean,
  ) => {
    if (updateType === WS_SUBTYPES.INIT && !syncAll) {
      throw new Error("syncAll must be true when sending SCENE.INIT");
    }

    // sync out only the elements we think we need to to save bandwidth.
    // periodically we'll resync the whole thing to make sure no one diverges
    // due to a dropped message (server goes down etc).
    const syncableElements = elements.reduce((acc, element) => {
      if (
        (syncAll ||
          !this.broadcastedElementVersions.has(element.id) ||
          element.version > this.broadcastedElementVersions.get(element.id)!) &&
        isSyncableElement(element)
      ) {
        acc.push(element);
      }
      return acc;
    }, [] as SyncableExcalidrawElement[]);

    const data: SocketUpdateDataSource[typeof updateType] = {
      type: updateType,
      payload: {
        elements: syncableElements,
      },
    };

    for (const syncableElement of syncableElements) {
      this.broadcastedElementVersions.set(
        syncableElement.id,
        syncableElement.version,
      );
    }

    this.queueFileUpload();

    await this._broadcastSocketData(data as SocketUpdateData);
  };

  broadcastIdleChange = (userState: UserIdleState) => {
    if (this.socket?.id) {
      const data: SocketUpdateDataSource["IDLE_STATUS"] = {
        type: WS_SUBTYPES.IDLE_STATUS,
        payload: {
          socketId: this.socket.id as SocketId,
          userState,
          username: this.collab.state.username,
        },
      };
      return this._broadcastSocketData(
        data as SocketUpdateData,
        true, // volatile
      );
    }
  };

  broadcastMouseLocation = (payload: {
    pointer: SocketUpdateDataSource["MOUSE_LOCATION"]["payload"]["pointer"];
    button: SocketUpdateDataSource["MOUSE_LOCATION"]["payload"]["button"];
  }) => {
    if (this.socket?.id) {
      const data: SocketUpdateDataSource["MOUSE_LOCATION"] = {
        type: WS_SUBTYPES.MOUSE_LOCATION,
        payload: {
          socketId: this.socket.id as SocketId,
          pointer: payload.pointer,
          button: payload.button || "up",
          selectedElementIds:
            this.collab.excalidrawAPI.getAppState().selectedElementIds,
          username: this.collab.state.username,
        },
      };

      return this._broadcastSocketData(
        data as SocketUpdateData,
        true, // volatile
      );
    }
  };

  broadcastVisibleSceneBounds = (
    payload: {
      sceneBounds: SocketUpdateDataSource["USER_VISIBLE_SCENE_BOUNDS"]["payload"]["sceneBounds"];
    },
    roomId: string,
  ) => {
    if (this.socket?.id) {
      const data: SocketUpdateDataSource["USER_VISIBLE_SCENE_BOUNDS"] = {
        type: WS_SUBTYPES.USER_VISIBLE_SCENE_BOUNDS,
        payload: {
          socketId: this.socket.id as SocketId,
          username: this.collab.state.username,
          sceneBounds: payload.sceneBounds,
        },
      };

      return this._broadcastSocketData(
        data as SocketUpdateData,
        true, // volatile
        roomId,
      );
    }
  };

  broadcastUserFollowed = (payload: OnUserFollowedPayload) => {
    if (this.socket?.id) {
      this.socket.emit(WS_EVENTS.USER_FOLLOW_CHANGE, payload);
    }
  };
}

export default Portal;
