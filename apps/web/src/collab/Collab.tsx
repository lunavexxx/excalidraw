import {
  CaptureUpdateAction,
  getSceneVersion,
  restoreElements,
  zoomToFitBounds,
  reconcileElements,
} from "@excalidraw/excalidraw";
import { clearAppStateForLocalStorage } from "@excalidraw/excalidraw/appState";
import { ErrorDialog } from "@excalidraw/excalidraw/components/ErrorDialog";
import { cloneJSON, EVENT, toBrandedType } from "@excalidraw/common";
import {
  IDLE_THRESHOLD,
  ACTIVE_THRESHOLD,
  UserIdleState,
  assertNever,
  isDevEnv,
  isTestEnv,
  preventUnload,
  resolvablePromise,
  throttleRAF,
} from "@excalidraw/common";
import { getVisibleSceneBounds } from "@excalidraw/element";
import { isInitializedImageElement } from "@excalidraw/element";
import { AbortError } from "@excalidraw/excalidraw/errors";
import { t } from "@excalidraw/excalidraw/i18n";
import { withBatchedUpdates } from "@excalidraw/excalidraw/reactUtils";

import throttle from "lodash.throttle";
import { PureComponent } from "react";

import { bumpElementVersions } from "@excalidraw/excalidraw/data/restore";

import type {
  ReconciledExcalidrawElement,
  RemoteExcalidrawElement,
} from "@excalidraw/excalidraw/data/reconcile";
import type { ImportedDataState } from "@excalidraw/excalidraw/data/types";
import type {
  ExcalidrawElement,
  FileId,
  InitializedExcalidrawImageElement,
  OrderedExcalidrawElement,
} from "@excalidraw/element/types";
import type {
  BinaryFileData,
  ExcalidrawImperativeAPI,
  SocketId,
  Collaborator,
  Gesture,
  UserToFollow,
} from "@excalidraw/excalidraw/types";
import type { Mutable, ValueOf } from "@excalidraw/common/utility-types";

import { appJotaiStore, atom } from "../app-jotai";
import {
  CURSOR_SYNC_TIMEOUT,
  INITIAL_SCENE_UPDATE_TIMEOUT,
  LOAD_IMAGES_TIMEOUT,
  SCENE_SEQ_PATROL_MS,
  SCENE_SYNC_MAX_ROUNDS,
  SCENE_SYNC_TIMEOUT,
  WS_SUBTYPES,
  WS_EVENTS,
} from "../app_constants";
import { getSyncableElements } from "../data";
import { FileManager, updateStaleImageStatuses } from "../data/FileManager";
import { FileStatusStore } from "../data/fileStatusStore";
import { LocalData } from "../data/LocalData";
import {
  importUsernameFromLocalStorage,
  saveUsernameToLocalStorage,
} from "../data/localStorage";

import {
  getGuestToken,
  getGuestShareToken,
  setGuestToken,
} from "../auth/guestSession";
import { refreshSession } from "../auth/api";
import {
  canvasIdAtom,
  canvasRealtimeStatusAtom,
  canvasRoleAtom,
  canvasCapabilitiesAtom,
  onlineUsersAtom,
  presenceConnectedAtom,
  collaborationRefreshAtom,
} from "../canvas/atoms";
import { getAccessToken } from "../auth/tokens";
import {
  exchangeShareToken,
  getCanvas,
  loadCanvasFiles,
  putCanvasFiles,
  saveCanvasScene,
} from "../canvas/api";
import { canvasSaver } from "../canvas/saver";

import { collabErrorIndicatorAtom } from "./CollabError";
import Portal from "./Portal";

import type { SceneSyncAck, SocketUpdateDataSource } from "../data";

export const collabAPIAtom = atom<CollabAPI | null>(null);
export const isCollaboratingAtom = atom(false);
export const isOfflineAtom = atom(false);

// 协议 v2 同步游标:cursor = 已连续应用的最大事件 seq;
// 乱序到达的帧暂存 gaps,补齐即推进(跨 pod 事件交错时保证补丁不漏)。
interface SeqCursorState {
  cursor: number;
  gaps: Set<number>;
}

interface CollabState {
  errorMessage: string | null;
  /** errors related to saving */
  dialogNotifiedErrors: Record<string, boolean>;
  username: string;
  activeRoomLink: string | null;
}

export const activeRoomLinkAtom = atom<string | null>(null);
export const userToFollowAtom = atom<UserToFollow | null>(null);

type CollabInstance = InstanceType<typeof Collab>;

export interface CollabAPI {
  /** function so that we can access the latest value from stale callbacks */
  isCollaborating: () => boolean;
  onPointerUpdate: CollabInstance["onPointerUpdate"];
  startCollaboration: CollabInstance["startCollaboration"];
  stopCollaboration: CollabInstance["stopCollaboration"];
  syncElements: CollabInstance["syncElements"];
  fetchImageFilesFromFirebase: CollabInstance["fetchImageFilesFromFirebase"];
  setUsername: CollabInstance["setUsername"];
  getUsername: CollabInstance["getUsername"];
  getActiveRoomLink: CollabInstance["getActiveRoomLink"];
  setCollabError: CollabInstance["setErrorDialog"];
  setUserToFollow: CollabInstance["setUserToFollow"];
  /** 协议 v2:冷启动同步游标注入(画布页打开时) */
  setServerSeqCursor: CollabInstance["setServerSeqCursor"];
}

interface CollabProps {
  excalidrawAPI: ExcalidrawImperativeAPI;
}

class Collab extends PureComponent<CollabProps, CollabState> {
  portal: Portal;
  fileManager: FileManager;
  excalidrawAPI: CollabProps["excalidrawAPI"];
  activeIntervalId: number | null;
  idleTimeoutId: number | null;

  private socketInitializationTimer?: number;
  private lastBroadcastedOrReceivedSceneVersion: number = -1;
  private collaborators = new Map<SocketId, Collaborator>();
  /** the socket ids of the users following the current user */
  private followedBy = new Set<SocketId>();

  // 协议 v2:每画布同步游标 + 补丁巡检(取代周期全量重播)
  private seqStates = new Map<string, SeqCursorState>();
  private seqPatrolTimer: number | null = null;
  private syncInFlight = false;
  private connectionGeneration = 0;
  private startingCanvasId: string | null = null;
  private resolveRoomInitialization: (() => void) | null = null;

  constructor(props: CollabProps) {
    super(props);
    this.state = {
      errorMessage: null,
      dialogNotifiedErrors: {},
      username: importUsernameFromLocalStorage() || "",
      activeRoomLink: null,
    };
    this.portal = new Portal(this);
    this.fileManager = new FileManager({
      onFileStatusChange: FileStatusStore.updateStatuses.bind(FileStatusStore),
      // 文件(内嵌图片)二进制从不过 room:上传/下载走画布文件 API
      // (与 M2 单人保存同一存储),内容 hash 寻址、幂等 upsert。
      getFiles: async (fileIds) => {
        const canvasId = this.portal.roomId;
        if (!canvasId) {
          throw new AbortError();
        }
        return loadCanvasFiles(canvasId, fileIds, {
          token: getGuestToken(canvasId),
        });
      },
      saveFiles: async ({ addedFiles }) => {
        const canvasId = this.portal.roomId;
        if (
          !canvasId ||
          getGuestToken(canvasId) ||
          appJotaiStore.get(canvasRoleAtom) === "viewer"
        ) {
          // guest 无 PUT 凭证:文件经房间广播保持在线,不落库
          throw new AbortError();
        }
        const files = [...addedFiles.values()].map((file) => {
          const base64 = file.dataURL.slice(file.dataURL.indexOf(",") + 1);
          return {
            file_id: file.id,
            mime_type: file.mimeType,
            data: base64,
          };
        });
        if (files.length === 0) {
          return {
            savedFiles: new Map<FileId, BinaryFileData>(),
            erroredFiles: new Map<FileId, BinaryFileData>(),
          };
        }
        await putCanvasFiles(canvasId, files);
        return {
          savedFiles: new Map(addedFiles),
          erroredFiles: new Map<FileId, BinaryFileData>(),
        };
      },
    });
    this.excalidrawAPI = props.excalidrawAPI;
    this.activeIntervalId = null;
    this.idleTimeoutId = null;
  }

  private onUmmount: (() => void) | null = null;

  componentDidMount() {
    window.addEventListener(EVENT.BEFORE_UNLOAD, this.beforeUnload);
    window.addEventListener("online", this.onOfflineStatusToggle);
    window.addEventListener("offline", this.onOfflineStatusToggle);
    window.addEventListener(EVENT.UNLOAD, this.onUnload);

    const unsubOnUserFollow = this.excalidrawAPI.onUserFollow((payload) => {
      this.setUserToFollow(
        payload.action === "FOLLOW" ? payload.userToFollow : null,
      );
    });
    const throttledRelayUserViewportBounds = throttleRAF(
      this.relayVisibleSceneBounds,
    );
    const unsubOnScrollChange = this.excalidrawAPI.onScrollChange(() =>
      throttledRelayUserViewportBounds(),
    );
    this.onUmmount = () => {
      unsubOnUserFollow();
      unsubOnScrollChange();
    };

    this.onOfflineStatusToggle();

    const collabAPI: CollabAPI = {
      isCollaborating: this.isCollaborating,
      onPointerUpdate: this.onPointerUpdate,
      startCollaboration: this.startCollaboration,
      syncElements: this.syncElements,
      fetchImageFilesFromFirebase: this.fetchImageFilesFromFirebase,
      stopCollaboration: this.stopCollaboration,
      setUsername: this.setUsername,
      getUsername: this.getUsername,
      getActiveRoomLink: this.getActiveRoomLink,
      setServerSeqCursor: this.setServerSeqCursor,
      setCollabError: this.setErrorDialog,
      setUserToFollow: this.setUserToFollow,
    };

    appJotaiStore.set(collabAPIAtom, collabAPI);

    if (isTestEnv() || isDevEnv()) {
      window.collab = window.collab || ({} as Window["collab"]);
      Object.defineProperties(window, {
        collab: {
          configurable: true,
          value: this,
        },
      });
    }
  }

  onOfflineStatusToggle = () => {
    appJotaiStore.set(isOfflineAtom, !window.navigator.onLine);
  };

  componentWillUnmount() {
    window.removeEventListener("online", this.onOfflineStatusToggle);
    window.removeEventListener("offline", this.onOfflineStatusToggle);
    window.removeEventListener(EVENT.BEFORE_UNLOAD, this.beforeUnload);
    window.removeEventListener(EVENT.UNLOAD, this.onUnload);
    window.removeEventListener(EVENT.POINTER_MOVE, this.onPointerMove);
    window.removeEventListener(
      EVENT.VISIBILITY_CHANGE,
      this.onVisibilityChange,
    );
    if (this.activeIntervalId) {
      window.clearInterval(this.activeIntervalId);
      this.activeIntervalId = null;
    }
    if (this.idleTimeoutId) {
      window.clearTimeout(this.idleTimeoutId);
      this.idleTimeoutId = null;
    }
    this.onUmmount?.();
    void this.stopCollaboration(false);
    appJotaiStore.set(collabAPIAtom, null);
  }

  isCollaborating = () => appJotaiStore.get(isCollaboratingAtom)!;

  private setIsCollaborating = (isCollaborating: boolean) => {
    appJotaiStore.set(isCollaboratingAtom, isCollaborating);
  };

  private onUnload = () => {
    this.destroySocketClient({ isUnload: true });
  };

  private beforeUnload = withBatchedUpdates((event: BeforeUnloadEvent) => {
    const syncableElements = getSyncableElements(
      this.getSceneElementsIncludingDeleted(),
    );

    // Warn before leaving with disconnected edits or pending image uploads.
    if (
      this.isCollaborating() &&
      (this.fileManager.shouldPreventUnload(syncableElements) ||
        (!this.excalidrawAPI.getAppState().viewModeEnabled &&
          getSceneVersion(syncableElements) >
            this.lastBroadcastedOrReceivedSceneVersion))
    ) {
      if (import.meta.env.VITE_APP_DISABLE_PREVENT_UNLOAD !== "true") {
        preventUnload(event);
      } else {
        console.warn(
          "preventing unload disabled (VITE_APP_DISABLE_PREVENT_UNLOAD)",
        );
      }
    }
  });

  // syncToServer 是协作会话的兜底整场景 PUT(折叠语义,幂等安全):
  // stopCollaboration 时调用,保证最终状态经 HTTP 通道落库一次
  // (即使 room 事件链路出现过缺口)。场景持久化主路径是 room 的事件流。
  private syncToServer = async (): Promise<void> => {
    const canvasId = this.portal.roomId;
    if (
      !canvasId ||
      getGuestToken(canvasId) ||
      this.excalidrawAPI.getAppState().viewModeEnabled
    ) {
      return;
    }
    try {
      const elements = getSyncableElements(
        this.getSceneElementsIncludingDeleted(),
      );
      // base_version 不再作为闸门(服务端折叠语义),传 0 即可。
      await saveCanvasScene(
        canvasId,
        {
          elements: cloneJSON(elements),
          appState: clearAppStateForLocalStorage(
            this.excalidrawAPI.getAppState(),
          ),
        },
        0,
      );
      this.resetErrorIndicator();
    } catch (error: any) {
      const errorMessage = /is longer than.*?bytes/.test(error.message)
        ? t("errors.collabSaveFailed_sizeExceeded")
        : t("errors.collabSaveFailed");

      if (
        !this.state.dialogNotifiedErrors[errorMessage] ||
        !this.isCollaborating()
      ) {
        this.setErrorDialog(errorMessage);
        this.setState({
          dialogNotifiedErrors: {
            ...this.state.dialogNotifiedErrors,
            [errorMessage]: true,
          },
        });
      }

      if (this.isCollaborating()) {
        this.setErrorIndicator(errorMessage);
      }

      console.error(error);
    }
  };

  stopCollaboration = async (keepRemoteState = true) => {
    this.connectionGeneration++;
    this.startingCanvasId = null;
    if (this.seqPatrolTimer !== null) {
      window.clearInterval(this.seqPatrolTimer);
      this.seqPatrolTimer = null;
    }
    this.loadImageFiles.cancel();
    this.resetErrorIndicator(true);

    const canvasId = this.portal.roomId;

    if (this.portal.socket && this.fallbackInitializationHandler) {
      this.portal.socket.off(
        "connect_error",
        this.fallbackInitializationHandler,
      );
    }

    if (!keepRemoteState) {
      LocalData.fileStorage.reset();
      this.destroySocketClient();
      return;
    }

    // 兜底整场景 PUT(折叠语义,幂等):即使 room 事件链路有缺口,
    // 最终状态也经 HTTP 通道落库一次。
    await this.syncToServer();
    this.destroySocketClient();

    if (canvasId && !getGuestToken(canvasId)) {
      // 协作期间 saver 静默(serverVersion 已陈旧),用服务端最新版本
      // 重新 adopt,恢复单人自动保存。
      try {
        const detail = await getCanvas(canvasId);
        canvasSaver.adopt({
          canvasId,
          version: detail.scene?.version ?? 0,
          name: detail.canvas.name,
          fileIds: detail.files.map((f) => f.file_id),
          role: detail.my_role === "viewer" ? "viewer" : "editor",
        });
      } catch (error) {
        // adopt 失败时下一次保存的冲突处理路径兜底
        console.error(error);
      }
    }
  };

  private destroySocketClient = (opts?: { isUnload: boolean }) => {
    this.connectionGeneration++;
    this.startingCanvasId = null;
    this.resolveRoomInitialization?.();
    this.resolveRoomInitialization = null;
    this.lastBroadcastedOrReceivedSceneVersion = -1;
    this.portal.close();
    appJotaiStore.set(canvasRealtimeStatusAtom, "idle");
    this.fileManager.reset();
    this.followedBy = new Set();
    if (!opts?.isUnload) {
      this.setIsCollaborating(false);
      this.setActiveRoomLink(null);
      appJotaiStore.set(userToFollowAtom, null);
      this.collaborators = new Map();
      this.excalidrawAPI.updateScene({
        collaborators: this.collaborators,
      });
      LocalData.resumeSave("collaboration");
    }
  };

  private fetchImageFilesFromFirebase = async (opts: {
    elements: readonly ExcalidrawElement[];
    /**
     * Indicates whether to fetch files that are errored or pending and older
     * than 10 seconds.
     *
     * Use this as a mechanism to fetch files which may be ok but for some
     * reason their status was not updated correctly.
     */
    forceFetchFiles?: boolean;
  }) => {
    const unfetchedImages = opts.elements
      .filter((element) => {
        return (
          isInitializedImageElement(element) &&
          !this.fileManager.isFileTracked(element.fileId) &&
          !element.isDeleted &&
          (opts.forceFetchFiles
            ? element.status !== "pending" ||
              Date.now() - element.updated > 10000
            : element.status === "saved")
        );
      })
      .map((element) => (element as InitializedExcalidrawImageElement).fileId);

    return await this.fileManager.getFiles(unfetchedImages);
  };

  // 明文广播:JSON 解码即可(传输由 TLS 保护)。
  private decodePayload = (
    data: ArrayBuffer,
  ): ValueOf<SocketUpdateDataSource> => {
    try {
      const decodedData = new TextDecoder("utf-8").decode(new Uint8Array(data));
      return JSON.parse(decodedData);
    } catch (error) {
      console.error(error);
      return {
        type: WS_SUBTYPES.INVALID_RESPONSE,
      };
    }
  };

  private fallbackInitializationHandler: null | (() => any) = null;

  // 协作房间 = 画布本身(/c/:id),无独立房间链接;入口为画布页分享对话框。
  // 场景初始同步不靠房间:画布页加载时已从服务端载入同源场景,这里只需
  // 连接 + 加入房间;首进房/重连时用服务端持久层快照做反熵补拉。
  startCollaboration = async (opts: {
    canvasId: string;
    /** presence 昵称(登录用户传账号昵称;guest 由调用方生成) */
    username?: string;
  }) => {
    if (opts.username && opts.username !== this.state.username) {
      this.setUsername(opts.username);
    }
    if (!this.state.username) {
      import("@excalidraw/random-username").then(({ getRandomUsername }) => {
        const username = getRandomUsername();
        this.setUsername(username);
      });
    }

    if (this.portal.socket || this.isCollaborating()) {
      if (
        this.portal.roomId === opts.canvasId &&
        this.portal.socket &&
        !this.portal.socket.connected &&
        !this.portal.socket.active
      ) {
        this.portal.socket.connect();
      }
      return null;
    }
    if (this.startingCanvasId) {
      return null;
    }
    const roomId = opts.canvasId;
    const generation = this.connectionGeneration;
    this.startingCanvasId = roomId;

    const scenePromise = resolvablePromise<
      | (ImportedDataState & { elements: readonly OrderedExcalidrawElement[] })
      | null
    >();

    let socketIOClient: typeof import("socket.io-client").default;
    try {
      await canvasSaver.flushAsync();
      socketIOClient = (await import("socket.io-client")).default;
    } catch (error) {
      if (generation === this.connectionGeneration) {
        this.startingCanvasId = null;
        appJotaiStore.set(canvasRealtimeStatusAtom, "reconnecting");
      }
      console.error(error);
      return null;
    }
    if (
      generation !== this.connectionGeneration ||
      appJotaiStore.get(canvasIdAtom) !== roomId
    ) {
      if (generation === this.connectionGeneration) {
        this.startingCanvasId = null;
      }
      return null;
    }
    this.startingCanvasId = null;
    canvasSaver.pauseForCollaboration();
    this.setIsCollaborating(true);
    appJotaiStore.set(canvasRealtimeStatusAtom, "connecting");
    LocalData.pauseSave("collaboration");
    this.resolveRoomInitialization = () => scenePromise.resolve(null);

    const fallbackInitializationHandler = () => {
      this.initializeRoom({ fetchScene: true }).then((scene) => {
        scenePromise.resolve(scene);
      });
    };
    this.fallbackInitializationHandler = fallbackInitializationHandler;

    try {
      this.portal.socket = this.portal.open(
        socketIOClient(import.meta.env.VITE_APP_WS_SERVER_URL || "/", {
          // 生产同源(经 Caddy /socket.io);websocket-only 免粘性会话
          transports: ["websocket"],
          // 回调形式:每次(重)连接取当前 token,access 过期后重连自动续
          auth: async (cb: (data: object) => void) => {
            try {
              const share = getGuestShareToken(roomId);
              if (share) {
                const guest = await exchangeShareToken(roomId, share);
                setGuestToken(roomId, guest.access_token);
              } else {
                await refreshSession();
              }
              cb({
                token: getGuestToken(roomId) ?? getAccessToken() ?? undefined,
              });
            } catch {
              cb({});
            }
          },
        }),
        roomId,
      );

      this.portal.socket.once("connect_error", fallbackInitializationHandler);
    } catch (error: any) {
      console.error(error);
      this.destroySocketClient();
      appJotaiStore.set(canvasRealtimeStatusAtom, "reconnecting");
      return null;
    }

    // 服务端拒绝进房(fail-closed):报错并退出协作
    this.portal.socket.on("join-room-error", (_payload: { reason: string }) => {
      this.setErrorDialog(t("errors.collabJoinForbidden"));
      void this.stopCollaboration(false);
    });

    // fallback in case you're not alone in the room but still don't receive
    // initial SCENE_INIT message
    this.socketInitializationTimer = window.setTimeout(
      fallbackInitializationHandler,
      INITIAL_SCENE_UPDATE_TIMEOUT,
    );

    // All socket listeners are moving to Portal
    this.portal.socket.on("client-broadcast", async (data: ArrayBuffer) => {
      const decodedData = this.decodePayload(data);

      switch (decodedData.type) {
        case WS_SUBTYPES.INVALID_RESPONSE:
          return;
        case WS_SUBTYPES.INIT: {
          if (!this.portal.socketInitialized) {
            this.initializeRoom({ fetchScene: false });
            const remoteElements = toBrandedType<
              readonly RemoteExcalidrawElement[]
            >(decodedData.payload.elements);
            const reconciledElements = this._reconcileElements(remoteElements);
            this.handleRemoteSceneUpdate(reconciledElements);
            // noop if already resolved via first-in-room fetch
            scenePromise.resolve({
              elements: reconciledElements,
              scrollToContent: true,
            });
          }
          break;
        }
        case WS_SUBTYPES.UPDATE:
          if (typeof decodedData.seq === "number" && this.portal.roomId) {
            this.observeSeq(this.portal.roomId, decodedData.seq);
          }
          this.handleRemoteSceneUpdate(
            this._reconcileElements(
              toBrandedType<readonly RemoteExcalidrawElement[]>(
                decodedData.payload.elements,
              ),
            ),
          );
          break;
        case WS_SUBTYPES.MOUSE_LOCATION: {
          const { pointer, button, username, selectedElementIds } =
            decodedData.payload;

          const socketId: SocketUpdateDataSource["MOUSE_LOCATION"]["payload"]["socketId"] =
            decodedData.payload.socketId ||
            // @ts-ignore legacy, see #2094 (#2097)
            decodedData.payload.socketID;

          this.updateCollaborator(socketId, {
            pointer,
            button,
            selectedElementIds,
            username: this.collaborators.get(socketId)?.id
              ? this.collaborators.get(socketId)?.username
              : username,
          });

          break;
        }

        case WS_SUBTYPES.USER_VISIBLE_SCENE_BOUNDS: {
          const { sceneBounds, socketId } = decodedData.payload;

          const userToFollow = appJotaiStore.get(userToFollowAtom);

          // we're not following the user
          // (shouldn't happen, but could be late message or bug upstream)
          if (userToFollow?.socketId !== socketId) {
            console.warn(
              `receiving remote client's (from ${socketId}) viewport bounds even though we're not subscribed to it!`,
            );
            return;
          }

          // cross-follow case, ignore updates in this case
          if (this.followedBy.has(userToFollow.socketId)) {
            return;
          }

          const appState = this.excalidrawAPI.getAppState();

          this.excalidrawAPI.updateScene({
            appState: zoomToFitBounds({
              appState,
              bounds: sceneBounds,
              fit: "contain",
            }).appState,
          });

          break;
        }

        case WS_SUBTYPES.IDLE_STATUS: {
          const { userState, socketId, username } = decodedData.payload;
          this.updateCollaborator(socketId, {
            userState,
            username: this.collaborators.get(socketId)?.id
              ? this.collaborators.get(socketId)?.username
              : username,
          });
          break;
        }

        default: {
          assertNever(decodedData, null);
        }
      }
    });

    this.portal.socket.on("first-in-room", async () => {
      if (!this.portal.roomJoined) {
        return;
      }
      const sceneData = await this.initializeRoom({ fetchScene: true });
      scenePromise.resolve(sceneData);
    });

    this.portal.socket.on(
      WS_EVENTS.USER_FOLLOW_ROOM_CHANGE,
      (followedBy: SocketId[]) => {
        this.followedBy = new Set(followedBy);

        this.relayVisibleSceneBounds({ force: true });
      },
    );

    this.initializeIdleDetector();

    // 游标巡检(协议 v2):低频探针,发现落后即拉补丁
    if (this.seqPatrolTimer === null) {
      this.seqPatrolTimer = window.setInterval(
        this.patrolSeq,
        SCENE_SEQ_PATROL_MS,
      );
    }

    this.setActiveRoomLink(window.location.href);

    return scenePromise;
  };

  // fetchScene:进房/重连/超时兜底的反熵补拉(协议 v2):
  // 优先 seq 补丁(sync-request,精确增量),通道不可用再回退 HTTP
  // 整场景(旧 room / API 不可达);两者都 reconcile,绝不盲覆盖,
  // 本地未广播的编辑由 bumpElementVersions 保护。
  private initializeRoom = async ({ fetchScene }: { fetchScene: boolean }) => {
    clearTimeout(this.socketInitializationTimer!);
    if (this.portal.socket && this.fallbackInitializationHandler) {
      this.portal.socket.off(
        "connect_error",
        this.fallbackInitializationHandler,
      );
    }
    const canvasId = this.portal.roomId;
    const socket = this.portal.socket;
    if (fetchScene && canvasId && socket) {
      try {
        const synced = await this.syncFromServerSeq(canvasId);
        if (!synced) {
          await this.fetchAndAdoptServerScene(canvasId);
        }
      } catch (error: any) {
        // log the error and move on. other peers will sync us the scene.
        console.error(error);
      } finally {
        if (this.portal.socket === socket && this.portal.roomId === canvasId) {
          this.portal.socketInitialized = this.portal.roomJoined;
        }
      }
    } else {
      this.portal.socketInitialized = this.portal.roomJoined;
    }
    return null;
  };

  // 冷启动 HTTP 装载(回退路径):整场景 + 冷启动游标
  private fetchAndAdoptServerScene = async (
    canvasId: string,
  ): Promise<void> => {
    const detail = await getCanvas(canvasId, {
      token: getGuestToken(canvasId),
    });
    if (this.portal.roomId !== canvasId) {
      return;
    }
    const remoteElements = detail.scene?.data?.elements;
    if (remoteElements && remoteElements.length > 0) {
      const restored = restoreElements(
        toBrandedType<readonly RemoteExcalidrawElement[]>(
          remoteElements as RemoteExcalidrawElement[],
        ),
        null,
      );
      this.handleRemoteSceneUpdate(this._reconcileElements(restored));
    }
    this.setServerSeqCursor(canvasId, detail.scene?.cursor ?? 0);
  };

  private seqState = (canvasId: string): SeqCursorState => {
    let s = this.seqStates.get(canvasId);
    if (!s) {
      s = { cursor: 0, gaps: new Set() };
      this.seqStates.set(canvasId, s);
    }
    return s;
  };

  /** 冷启动游标注入(画布页打开时由 openServerCanvas 调用) */
  setServerSeqCursor = (canvasId: string, cursor: number): void => {
    if (Number.isSafeInteger(cursor) && cursor >= 0) {
      const s = this.seqState(canvasId);
      s.cursor = Math.max(s.cursor, cursor);
    }
  };

  // 收到带 seq 的增量帧:推进连续游标(乱序暂存,补齐即推进)
  private observeSeq = (canvasId: string, seq: number): void => {
    const s = this.seqState(canvasId);
    if (seq <= s.cursor || s.gaps.has(seq)) {
      return;
    }
    if (seq === s.cursor + 1) {
      s.cursor = seq;
      while (s.gaps.delete(s.cursor + 1)) {
        s.cursor += 1;
      }
    } else {
      s.gaps.add(seq);
    }
  };

  // Sync Step 1/2:以游标请求补丁直到收敛;
  // 返回 false = 补丁通道不可用(调用方回退 HTTP 整场景)
  private syncFromServerSeq = async (canvasId: string): Promise<boolean> => {
    const socket = this.portal.socket;
    if (!socket || !socket.connected) {
      return false;
    }
    if (this.syncInFlight) {
      return true; // 同步已在途,其补丁覆盖本次需求
    }
    this.syncInFlight = true;
    try {
      for (let round = 0; round < SCENE_SYNC_MAX_ROUNDS; round++) {
        const patch = await this.requestScenePatch(
          canvasId,
          this.seqState(canvasId).cursor,
        );
        if (!patch || "error" in patch) {
          return false;
        }
        if (this.portal.socket !== socket || this.portal.roomId !== canvasId) {
          return false;
        }
        if (patch.elements.length > 0) {
          const restored = restoreElements(
            toBrandedType<readonly RemoteExcalidrawElement[]>(
              patch.elements as RemoteExcalidrawElement[],
            ),
            null,
          );
          this.handleRemoteSceneUpdate(this._reconcileElements(restored));
        }
        const s = this.seqState(canvasId);
        s.cursor = Math.max(s.cursor, patch.cursor);
        if (!patch.has_more) {
          return true;
        }
      }
      // 超过分页上限:本轮放弃,下次巡检继续收敛
      return true;
    } finally {
      this.syncInFlight = false;
    }
  };

  private requestScenePatch = (
    canvasId: string,
    afterSeq: number,
  ): Promise<SceneSyncAck | null> => {
    return new Promise((resolve) => {
      const socket = this.portal.socket;
      if (!socket) {
        resolve(null);
        return;
      }
      socket
        .timeout(SCENE_SYNC_TIMEOUT)
        .emit(
          "sync-request",
          canvasId,
          { after_seq: afterSeq },
          (err: Error | null, res?: SceneSyncAck) => {
            resolve(err || !res ? null : res);
          },
        );
    });
  };

  // 巡检:低频探针,游标落后即拉补丁(远程完整性;断帧/乱序自愈)
  private patrolSeq = (): void => {
    const canvasId = this.portal.roomId;
    if (this.isCollaborating() && canvasId && this.portal.socket?.connected) {
      void this.syncFromServerSeq(canvasId)
        .then(() => this.syncElements(this.getSceneElementsIncludingDeleted()))
        .catch(() => undefined);
    }
  };

  /** Every successful join reconciles remote changes and sends pending edits. */
  onRoomJoined = async (): Promise<void> => {
    const socket = this.portal.socket;
    const canvasId = this.portal.roomId;
    await this.initializeRoom({ fetchScene: true });
    if (
      this.portal.socket !== socket ||
      this.portal.roomId !== canvasId ||
      !this.portal.isOpen()
    ) {
      return;
    }
    this.resolveRoomInitialization?.();
    this.resolveRoomInitialization = null;
    if (!this.excalidrawAPI.getAppState().viewModeEnabled) {
      const elements = this.getSceneElementsIncludingDeleted();
      if (elements.length) {
        await this.portal.broadcastScene(WS_SUBTYPES.UPDATE, elements, true);
        this.lastBroadcastedOrReceivedSceneVersion = getSceneVersion(elements);
      }
    }
  };

  /** 自写帧落库确认:seq 参与游标推进(自编辑内容本地已应用,等价 gap 闭合),并清除降级提示 */
  onOwnPersistedSeq = (canvasId: string, seq: number): void => {
    if (canvasId === this.portal.roomId) {
      this.observeSeq(canvasId, seq);
      this.resetErrorIndicator();
    }
  };

  /** 自写帧未落库(队列溢出/DB 故障):显式提示降级,巡检+兜底 PUT 自愈 */
  onOwnPersistFailed = (canvasId: string): void => {
    if (canvasId === this.portal.roomId && this.isCollaborating()) {
      this.portal.broadcastedElementVersions.clear();
      this.lastBroadcastedOrReceivedSceneVersion = -1;
      this.setErrorIndicator(t("errors.collabSyncDegraded"));
    }
  };

  private _reconcileElements = (
    remoteElements: readonly RemoteExcalidrawElement[],
  ): ReconciledExcalidrawElement[] => {
    const appState = this.excalidrawAPI.getAppState();

    const existingElements = this.getSceneElementsIncludingDeleted();

    // NOTE ideally we restore _after_ reconciliation but we can't do that
    // as we'd regenerate even elements such as appState.newElement which would
    // break the state
    remoteElements = restoreElements(remoteElements, existingElements);

    let reconciledElements = reconcileElements(
      existingElements,
      remoteElements,
      appState,
    );

    reconciledElements = bumpElementVersions(
      reconciledElements,
      existingElements,
    );

    // Avoid broadcasting to the rest of the collaborators the scene
    // we just received!
    // Note: this needs to be set before updating the scene as it
    // synchronously calls render.
    this.setLastBroadcastedOrReceivedSceneVersion(
      getSceneVersion(reconciledElements),
    );

    return reconciledElements;
  };

  private loadImageFiles = throttle(async () => {
    const { loadedFiles, erroredFiles } =
      await this.fetchImageFilesFromFirebase({
        elements: this.excalidrawAPI.getSceneElementsIncludingDeleted(),
      });

    this.excalidrawAPI.addFiles(loadedFiles);

    updateStaleImageStatuses({
      excalidrawAPI: this.excalidrawAPI,
      erroredFiles,
      elements: this.excalidrawAPI.getSceneElementsIncludingDeleted(),
    });
  }, LOAD_IMAGES_TIMEOUT);

  private handleRemoteSceneUpdate = (
    elements: ReconciledExcalidrawElement[],
  ) => {
    this.excalidrawAPI.updateScene({
      elements,
      captureUpdate: CaptureUpdateAction.NEVER,
    });

    this.loadImageFiles();
  };

  private onPointerMove = () => {
    if (this.idleTimeoutId) {
      window.clearTimeout(this.idleTimeoutId);
      this.idleTimeoutId = null;
    }

    this.idleTimeoutId = window.setTimeout(this.reportIdle, IDLE_THRESHOLD);

    if (!this.activeIntervalId) {
      this.activeIntervalId = window.setInterval(
        this.reportActive,
        ACTIVE_THRESHOLD,
      );
    }
  };

  private onVisibilityChange = () => {
    if (document.hidden) {
      if (this.idleTimeoutId) {
        window.clearTimeout(this.idleTimeoutId);
        this.idleTimeoutId = null;
      }
      if (this.activeIntervalId) {
        window.clearInterval(this.activeIntervalId);
        this.activeIntervalId = null;
      }
      this.onIdleStateChange(UserIdleState.AWAY);
    } else {
      this.idleTimeoutId = window.setTimeout(this.reportIdle, IDLE_THRESHOLD);
      this.activeIntervalId = window.setInterval(
        this.reportActive,
        ACTIVE_THRESHOLD,
      );
      this.onIdleStateChange(UserIdleState.ACTIVE);
    }
  };

  private reportIdle = () => {
    this.onIdleStateChange(UserIdleState.IDLE);
    if (this.activeIntervalId) {
      window.clearInterval(this.activeIntervalId);
      this.activeIntervalId = null;
    }
  };

  private reportActive = () => {
    this.onIdleStateChange(UserIdleState.ACTIVE);
  };

  private initializeIdleDetector = () => {
    document.addEventListener(EVENT.POINTER_MOVE, this.onPointerMove);
    document.addEventListener(EVENT.VISIBILITY_CHANGE, this.onVisibilityChange);
  };

  setPresence(
    people: {
      socket_id: SocketId;
      user_id: string;
      nickname: string;
      avatar_url: string;
      role: string;
    }[],
  ) {
    const users = new Map<
      string,
      { user_id: string; nickname: string; avatar_url: string; role: string }
    >();
    this.setCollaborators(people.map((p) => p.socket_id));
    for (const p of people) {
      users.set(p.socket_id, p);
      this.updateCollaborator(p.socket_id, {
        id: p.user_id,
        username: p.nickname || "Guest",
        avatarUrl: p.avatar_url,
      });
    }
    appJotaiStore.set(onlineUsersAtom, users);
  }

  onAccessChanged = async (access: {
    canvas_id: string;
    role: string;
    unavailable?: boolean;
  }) => {
    if (access.canvas_id !== this.portal.roomId) {
      return;
    }
    if (access.unavailable) {
      appJotaiStore.set(canvasRoleAtom, "viewer");
      appJotaiStore.set(presenceConnectedAtom, false);
      appJotaiStore.set(canvasRealtimeStatusAtom, "reconnecting");
      appJotaiStore.set(canvasCapabilitiesAtom, {
        can_manage_collaborators: false,
        can_manage_share_links: false,
        can_review_requests: false,
      });
      this.excalidrawAPI.updateScene({
        appState: {
          viewModeEnabled: true,
          errorMessage: t("collabAccess.permissionUnavailable"),
        },
      });
      return;
    }
    appJotaiStore.set(presenceConnectedAtom, true);
    appJotaiStore.set(canvasRealtimeStatusAtom, "connected");
    if (!access.role) {
      appJotaiStore.set(canvasRoleAtom, "viewer");
      appJotaiStore.set(canvasCapabilitiesAtom, {
        can_manage_collaborators: false,
        can_manage_share_links: false,
        can_review_requests: false,
      });
      this.excalidrawAPI.updateScene({ appState: { viewModeEnabled: true } });
      await this.stopCollaboration(false);
      canvasSaver.detach();
      this.excalidrawAPI.resetScene();
      this.excalidrawAPI.updateScene({
        appState: {
          viewModeEnabled: true,
          errorMessage: t("collabAccess.accessLost"),
        },
      });
      return;
    }
    const guest = !!getGuestToken(access.canvas_id);
    appJotaiStore.set(
      canvasRoleAtom,
      guest ? "guest" : (access.role as "owner" | "editor" | "viewer"),
    );
    this.excalidrawAPI.updateScene({
      appState: {
        viewModeEnabled: access.role === "viewer",
        errorMessage:
          this.excalidrawAPI.getAppState().errorMessage ===
          t("collabAccess.permissionUnavailable")
            ? null
            : this.excalidrawAPI.getAppState().errorMessage,
      },
    });
    if (!guest) {
      try {
        const detail = await getCanvas(access.canvas_id);
        if (access.canvas_id !== this.portal.roomId) {
          return;
        }
        if (detail.capabilities) {
          appJotaiStore.set(canvasCapabilitiesAtom, detail.capabilities);
        }
        canvasSaver.adopt({
          canvasId: access.canvas_id,
          version: detail.scene?.version ?? 0,
          name: detail.canvas.name,
          fileIds: detail.files.map((f) => f.file_id),
          role: access.role === "viewer" ? "viewer" : "editor",
        });
      } catch {
        /* room already enforces the updated permission */
      }
    }
    appJotaiStore.set(collaborationRefreshAtom, (n) => n + 1);
  };

  setCollaborators(sockets: SocketId[]) {
    const collaborators: InstanceType<typeof Collab>["collaborators"] =
      new Map();
    for (const socketId of sockets) {
      const isCurrentUser = socketId === this.portal.socket?.id;
      collaborators.set(
        socketId,
        Object.assign(
          // we never receive our own broadcasts, so we need to seed
          // our own collaborator entry with the local username
          isCurrentUser ? { username: this.state.username } : {},
          this.collaborators.get(socketId),
          { isCurrentUser },
        ),
      );
    }
    this.collaborators = collaborators;
    this.excalidrawAPI.updateScene({ collaborators });

    // unfollow if the followed user left the room
    const userToFollow = appJotaiStore.get(userToFollowAtom);
    if (userToFollow && !collaborators.has(userToFollow.socketId)) {
      this.setUserToFollow(null);
    }
  }

  updateCollaborator = (socketId: SocketId, updates: Partial<Collaborator>) => {
    // Pointer payloads cannot introduce accounts absent from the server roster.
    if (!this.collaborators.has(socketId)) {
      return;
    }
    const isCurrentUser = socketId === this.portal.socket?.id;
    const collaborators = new Map(this.collaborators);
    const user: Mutable<Collaborator> = Object.assign(
      // we never receive our own broadcasts, so we need to seed
      // our own collaborator entry with the local username
      isCurrentUser ? { username: this.state.username } : {},
      collaborators.get(socketId),
      updates,
      { isCurrentUser },
    );
    collaborators.set(socketId, user);
    this.collaborators = collaborators;

    this.excalidrawAPI.updateScene({
      collaborators,
    });
  };

  public setLastBroadcastedOrReceivedSceneVersion = (version: number) => {
    this.lastBroadcastedOrReceivedSceneVersion = version;
  };

  public getLastBroadcastedOrReceivedSceneVersion = () => {
    return this.lastBroadcastedOrReceivedSceneVersion;
  };

  public getSceneElementsIncludingDeleted = () => {
    return this.excalidrawAPI.getSceneElementsIncludingDeleted();
  };

  onPointerUpdate = throttle(
    (payload: {
      pointer: SocketUpdateDataSource["MOUSE_LOCATION"]["payload"]["pointer"];
      button: SocketUpdateDataSource["MOUSE_LOCATION"]["payload"]["button"];
      pointersMap: Gesture["pointers"];
    }) => {
      payload.pointersMap.size < 2 &&
        this.portal.socket &&
        this.portal.broadcastMouseLocation(payload);
    },
    CURSOR_SYNC_TIMEOUT,
  );

  relayVisibleSceneBounds = (props?: { force: boolean }) => {
    if (this.portal.socket && (this.followedBy.size > 0 || props?.force)) {
      this.portal.broadcastVisibleSceneBounds(
        {
          sceneBounds: getVisibleSceneBounds(this.excalidrawAPI.getAppState()),
        },
        `follow@${this.portal.socket.id}`,
      );
    }
  };

  onIdleStateChange = (userState: UserIdleState) => {
    this.portal.broadcastIdleChange(userState);
  };

  broadcastElements = (elements: readonly OrderedExcalidrawElement[]) => {
    if (!this.portal.isOpen()) {
      return;
    }
    if (
      getSceneVersion(elements) >
      this.getLastBroadcastedOrReceivedSceneVersion()
    ) {
      this.portal.broadcastScene(WS_SUBTYPES.UPDATE, elements, false);
      this.lastBroadcastedOrReceivedSceneVersion = getSceneVersion(elements);
      // 协议 v2:不再周期全量重播,反熵由游标巡检 + 状态补丁承担
    }
  };

  syncElements = (elements: readonly OrderedExcalidrawElement[]) => {
    if (this.excalidrawAPI.getAppState().viewModeEnabled) {
      return;
    }
    // 场景持久化 = room 先落库后转发(帧携带 seq),客户端无保存循环
    this.broadcastElements(elements);
  };

  setUserToFollow = (userToFollow: UserToFollow | null) => {
    const prev = appJotaiStore.get(userToFollowAtom) ?? null;

    if (prev?.socketId !== userToFollow?.socketId && this.portal.socket) {
      // leave the previous user's follow room before joining the next one
      if (prev) {
        this.portal.broadcastUserFollowed({
          userToFollow: prev,
          action: "UNFOLLOW",
        });
      }
      if (userToFollow) {
        this.portal.broadcastUserFollowed({
          userToFollow,
          action: "FOLLOW",
        });
      }
    }

    appJotaiStore.set(userToFollowAtom, userToFollow);
  };

  setUsername = (username: string) => {
    this.setState({ username });
    saveUsernameToLocalStorage(username);

    // keep our own collaborator entry in sync
    const socketId = this.portal.socket?.id as SocketId | undefined;
    if (socketId && this.collaborators.has(socketId)) {
      this.updateCollaborator(socketId, { username });
    }
  };

  getUsername = () => this.state.username;

  setActiveRoomLink = (activeRoomLink: string | null) => {
    this.setState({ activeRoomLink });
    appJotaiStore.set(activeRoomLinkAtom, activeRoomLink);
  };

  getActiveRoomLink = () => this.state.activeRoomLink;

  setErrorIndicator = (errorMessage: string | null) => {
    appJotaiStore.set(collabErrorIndicatorAtom, {
      message: errorMessage,
      nonce: Date.now(),
    });
  };

  resetErrorIndicator = (resetDialogNotifiedErrors = false) => {
    appJotaiStore.set(collabErrorIndicatorAtom, { message: null, nonce: 0 });
    if (resetDialogNotifiedErrors) {
      this.setState({
        dialogNotifiedErrors: {},
      });
    }
  };

  setErrorDialog = (errorMessage: string | null) => {
    this.setState({
      errorMessage,
    });
  };

  render() {
    const { errorMessage } = this.state;

    return (
      <>
        {errorMessage != null && (
          <ErrorDialog onClose={() => this.setErrorDialog(null)}>
            {errorMessage}
          </ErrorDialog>
        )}
      </>
    );
  }
}

declare global {
  interface Window {
    collab: InstanceType<typeof Collab>;
  }
}

if (isTestEnv() || isDevEnv()) {
  window.collab = window.collab || ({} as Window["collab"]);
}

export default Collab;

export type TCollabClass = Collab;
