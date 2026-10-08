import { CaptureUpdateAction, restoreElements } from "@excalidraw/excalidraw";
import { APP_NAME, debounce } from "@excalidraw/common";
import { clearAppStateForLocalStorage } from "@excalidraw/excalidraw/appState";
import {
  reconcileElements,
  type RemoteExcalidrawElement,
} from "@excalidraw/excalidraw/data/reconcile";
import { t } from "@excalidraw/excalidraw/i18n";
import { exportToCanvas } from "@excalidraw/utils";

import type {
  AppState,
  BinaryFiles,
  ExcalidrawImperativeAPI,
} from "@excalidraw/excalidraw/types";
import type { OrderedExcalidrawElement } from "@excalidraw/element/types";

import { appJotaiStore } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";

import { LocalData } from "../data/LocalData";

import {
  canvasIdAtom,
  canvasRoleAtom,
  canvasCapabilitiesAtom,
  canvasSaveErrorAtom,
  canvasSaveStateAtom,
  draftDirtyAtom,
} from "./atoms";
import {
  createCanvas,
  deleteCanvas,
  getCanvas,
  putCanvasFiles,
  saveCanvasScene,
  renameCanvas,
} from "./api";
import { clearLocalDraft } from "./localDraft";

import type { CanvasFilePayload } from "./types";

const SAVE_DEBOUNCE_MS = 2000;
const SAVE_MAX_WAIT_MS = 15000;
const RENAME_DEBOUNCE_MS = 800;
const RETRY_DELAYS_MS = [1000, 4000, 16000];
const THUMBNAIL_INTERVAL_MS = 60_000;
const FLUSH_WAIT_MS = 3000;

type SavePayload = {
  elements: readonly OrderedExcalidrawElement[];
  appState: AppState;
  files: BinaryFiles;
};

/**
 * 归一化发送给服务端的画布名:空白/上游占位名(labels.untitled-<时间戳>,
 * 组件初始化产物而非用户意图)一律返回 undefined,交给服务端默认名。
 */
export const normalizeSentName = (
  name: string | null | undefined,
): string | undefined => {
  const trimmed = name?.trim();
  if (!trimmed) {
    return undefined;
  }
  if (trimmed.startsWith(`${t("labels.untitled")}-`)) {
    return undefined;
  }
  return trimmed;
};

/**
 * Bootstrap new online canvases and flush HTTP work before realtime takes over.
 * Existing online canvases use the room's persistence and synchronization;
 * adopt() temporarily restores this saver when switching away from a room.
 */
class CanvasSaver {
  private excalidrawAPI: ExcalidrawImperativeAPI | null = null;

  private canvasId: string | null = null;
  private serverVersion = 0;
  private savedName: string | null = null;
  private syncedFileIds = new Set<string>();
  /** 内容写权限;viewer/guest 打开时 saver 完全静默(PUT 会 403)。 */
  private role: "editor" | "viewer" = "editor";

  private pending: SavePayload | null = null;
  private firstPendingAt = 0;
  private saveInFlight = false;
  private savePromise: Promise<void> | null = null;
  /** 本会话(adopt 以来)是否见过带内容的场景;空场景兜底判据。 */
  private seenContent = false;
  private retryAttempt = 0;
  private conflictRetries = 0;
  private disposed = false;
  private lastThumbnailAt = 0;
  /**
   * adopt/detach 递增;在途保存跨越代次后作废,防止页内切换画布时
   * 旧场景数据 PUT 到新 canvasId(或孤儿懒创建覆盖 URL/atom)。
   */
  private generation = 0;
  private pendingThumbnail: string | undefined;

  init(excalidrawAPI: ExcalidrawImperativeAPI) {
    if (!this.excalidrawAPI) {
      this.excalidrawAPI = excalidrawAPI;
    }
  }

  /** 打开服务端画布后,以服务端状态为基准接管保存。 */
  adopt(params: {
    canvasId: string;
    version: number;
    name: string;
    fileIds: Iterable<string>;
    role?: "editor" | "viewer";
  }) {
    this.generation++;
    this.canvasId = params.canvasId;
    this.serverVersion = params.version;
    this.savedName = params.name;
    this.syncedFileIds = new Set(params.fileIds);
    this.role = params.role ?? "editor";
    this.conflictRetries = 0;
    this.retryAttempt = 0;
    this.pending = null;
    this.firstPendingAt = 0;
    this.pendingThumbnail = undefined;
    this.seenContent = false;
    appJotaiStore.set(canvasIdAtom, params.canvasId);
    appJotaiStore.set(draftDirtyAtom, false);
    this.setState("idle");
  }

  /** The room owns persistence after pending HTTP saves have been flushed. */
  pauseForCollaboration() {
    this.generation++;
    this.pending = null;
    this.firstPendingAt = 0;
    this.role = "viewer";
    this.scheduleSave.cancel();
    this.scheduleRename.cancel();
  }

  /** 面板等外部途径完成重命名后同步基准,避免保存器重复 PATCH。 */
  noteServerName(name: string) {
    if (this.canvasId) {
      this.savedName = name;
    }
  }

  /** 回到未落库的本地草稿状态(新建画布/回到首页)。 */
  detach() {
    this.generation++;
    this.canvasId = null;
    this.serverVersion = 0;
    this.savedName = null;
    this.syncedFileIds = new Set();
    this.role = "editor";
    this.pending = null;
    this.firstPendingAt = 0;
    this.pendingThumbnail = undefined;
    this.conflictRetries = 0;
    this.retryAttempt = 0;
    this.seenContent = false;
    appJotaiStore.set(canvasIdAtom, null);
    appJotaiStore.set(draftDirtyAtom, false);
    this.setState("idle");
  }

  /**
   * 等待在途/已挂起的保存完成(切换画布等离开场景前调用);
   * 不追退避重试链,超时放行由调用方决定去留。
   */
  async flushAsync(): Promise<void> {
    this.scheduleRename.flush();
    if (this.saveInFlight) {
      await Promise.race([
        this.savePromise ?? Promise.resolve(),
        this.wait(FLUSH_WAIT_MS),
      ]);
      return;
    }
    this.scheduleSave.flush();
    if (this.saveInFlight) {
      await Promise.race([
        this.savePromise ?? Promise.resolve(),
        this.wait(FLUSH_WAIT_MS),
      ]);
    }
  }

  private wait(ms: number): Promise<void> {
    return new Promise((resolve) => {
      window.setTimeout(resolve, ms);
    });
  }

  /** onChange 热路径入口:只记录引用 + 调度防抖,不做 IO。 */
  queueSave(
    elements: readonly OrderedExcalidrawElement[],
    appState: AppState,
    files: BinaryFiles,
  ) {
    if (!this.excalidrawAPI || !appJotaiStore.get(currentUserAtom)) {
      return;
    }
    // viewer/guest 打开的画布只读:不排队、不上送(服务端会 42002)
    if (this.role === "viewer") {
      return;
    }
    this.pending = { elements, appState, files };

    const hasContent = elements.some((el) => !el.isDeleted);
    if (!this.canvasId) {
      appJotaiStore.set(draftDirtyAtom, hasContent);
    }
    if (hasContent) {
      this.seenContent = true;
    }

    // 仅在用户显式命名时同步重命名;空名/上游占位名不回写,
    // 避免把服务端默认名刷成 Untitled-<时间戳>
    const name = normalizeSentName(appState.name);
    if (
      this.canvasId &&
      name &&
      this.savedName !== null &&
      name !== this.savedName
    ) {
      this.scheduleRename(name);
    }
    // 防抖之外强制 15s 上限,连续绘制不会无限推迟上送
    const now = Date.now();
    if (!this.firstPendingAt) {
      this.firstPendingAt = now;
    }
    if (now - this.firstPendingAt >= SAVE_MAX_WAIT_MS) {
      this.scheduleSave.flush();
    } else {
      this.scheduleSave();
    }
  }

  hasPendingWork() {
    return this.pending !== null || this.saveInFlight;
  }

  flush() {
    this.scheduleSave.flush();
    this.scheduleRename.flush();
  }

  private scheduleSave = debounce(() => {
    void this.runSave();
  }, SAVE_DEBOUNCE_MS);

  private scheduleRename = debounce((name: string) => {
    if (!this.canvasId) {
      return;
    }
    renameCanvas(this.canvasId, name)
      .then(() => {
        this.savedName = name;
      })
      .catch(() => {
        // 重命名失败不打断保存流;下次 onChange 再试
      });
  }, RENAME_DEBOUNCE_MS);

  private setState(
    state: "idle" | "saving" | "error",
    errorKey: string | null = null,
  ) {
    appJotaiStore.set(canvasSaveStateAtom, state);
    appJotaiStore.set(canvasSaveErrorAtom, errorKey);
  }

  private runSave(): Promise<void> {
    const promise = this.doRunSave();
    this.savePromise = promise;
    return promise;
  }

  private async doRunSave(): Promise<void> {
    const generation = this.generation;
    const payload = this.pending;
    if (!payload || !this.excalidrawAPI || this.saveInFlight || this.disposed) {
      return;
    }
    if (!appJotaiStore.get(currentUserAtom)) {
      return;
    }
    this.saveInFlight = true;
    try {
      if (!this.canvasId) {
        const hasContent = payload.elements.some((el) => !el.isDeleted);
        if (!hasContent) {
          // 空白画布不落库(懒创建)
          this.pending = null;
          return;
        }
        const canvas = await createCanvas(
          normalizeSentName(payload.appState.name),
        );
        if (generation !== this.generation) {
          // 保存中途切走:删掉孤儿画布,旧内容不写入任何画布
          void deleteCanvas(canvas.id).catch(() => {});
          return;
        }
        this.canvasId = canvas.id;
        this.serverVersion = 0;
        this.savedName = canvas.name;
        this.syncedFileIds = new Set();
        appJotaiStore.set(canvasIdAtom, canvas.id);
        appJotaiStore.set(canvasRoleAtom, "owner");
        appJotaiStore.set(canvasCapabilitiesAtom, {
          can_manage_collaborators: true,
          can_manage_share_links: true,
          can_review_requests: true,
        });
        window.history.replaceState({}, APP_NAME, `/c/${canvas.id}`);
        // 懒创建成功=首页草稿被隐式导入:清掉冻结草稿防重复导入;
        // 先 flush(登录态为 files-only)再清,防 debounce 陈旧参数复活草稿
        LocalData.flushSave();
        clearLocalDraft();
      } else if (
        !payload.elements.some((el) => !el.isDeleted) &&
        !this.seenContent
      ) {
        // 兜底:本会话从未见过带内容的场景,空快照只可能是初始化窗口
        // 或乱序 onChange,落库会把云端画布清空,直接丢弃
        this.pending = null;
        return;
      }

      this.setState("saving");

      const newFiles = this.collectNewFiles(payload.files);
      if (newFiles.length) {
        await putCanvasFiles(this.canvasId, newFiles);
        if (generation !== this.generation) {
          return;
        }
        for (const f of newFiles) {
          this.syncedFileIds.add(f.file_id);
        }
      }

      const version = await this.putScene(payload);
      if (generation !== this.generation) {
        return;
      }
      this.serverVersion = version;
      this.savedName =
        normalizeSentName(payload.appState.name) ?? this.savedName;
      this.pending = null;
      this.firstPendingAt = 0;
      this.conflictRetries = 0;
      this.retryAttempt = 0;
      this.setState("idle");
    } catch (error: any) {
      if (generation !== this.generation) {
        return;
      }
      this.handleSaveError(error);
    } finally {
      this.saveInFlight = false;
    }
  }

  private async putScene(payload: SavePayload): Promise<number> {
    this.queueThumbnail();
    const thumbnail = this.pendingThumbnail;
    this.pendingThumbnail = undefined;
    const { version } = await saveCanvasScene(
      this.canvasId!,
      {
        elements: payload.elements as unknown[],
        appState: clearAppStateForLocalStorage(payload.appState),
      },
      this.serverVersion,
      thumbnail,
    );
    return version;
  }

  private collectNewFiles(files: BinaryFiles): CanvasFilePayload[] {
    const payloads: CanvasFilePayload[] = [];
    for (const [id, file] of Object.entries(files)) {
      if (this.syncedFileIds.has(id)) {
        continue;
      }
      const dataURL: string = (file as { dataURL?: string }).dataURL ?? "";
      const base64 = dataURL.split(",")[1];
      if (!base64) {
        continue;
      }
      payloads.push({
        file_id: id,
        mime_type: (file as { mimeType?: string }).mimeType || "image/png",
        data: base64,
      });
    }
    // 后端单请求有数量上限,超出部分随下一次防抖保存继续上送
    return payloads.slice(0, 50);
  }

  private handleSaveError(error: any) {
    const code = error?.code as number | undefined;
    if (code === 41001) {
      // 画布被删除/无权限:回到本地草稿
      this.detach();
      window.history.replaceState({}, APP_NAME, "/");
      this.setState("error", "canvasPanel.notFound");
      return;
    }
    if (code === 41002) {
      void this.resolveConflict();
      return;
    }
    // 瞬时错误(网络/5xx):指数退避重试
    if (this.retryAttempt < RETRY_DELAYS_MS.length) {
      const delay = RETRY_DELAYS_MS[this.retryAttempt++];
      window.setTimeout(() => {
        if (!this.disposed && this.pending) {
          void this.runSave();
        }
      }, delay);
      return;
    }
    this.setState("error", "canvasPanel.saveFailed");
  }

  /** 版本冲突:拉服务端场景与本地 reconcile 合并,再用新版本重放保存。 */
  private async resolveConflict() {
    if (!this.excalidrawAPI || !this.canvasId) {
      return;
    }
    const generation = this.generation;
    try {
      const remote = await getCanvas(this.canvasId);
      if (generation !== this.generation) {
        return;
      }
      const remoteData = remote.scene?.data as
        | { elements: RemoteExcalidrawElement[] }
        | undefined;

      const localElements =
        this.excalidrawAPI.getSceneElementsIncludingDeleted();
      const localAppState = this.excalidrawAPI.getAppState();

      if (remoteData?.elements?.length) {
        const restoredRemote = restoreElements(
          remoteData.elements,
          localElements,
        );
        const reconciled = reconcileElements(
          localElements,
          restoredRemote,
          localAppState,
        );
        this.excalidrawAPI.updateScene({
          elements: reconciled,
          captureUpdate: CaptureUpdateAction.NEVER,
        });
      }

      this.serverVersion = remote.scene?.version ?? 0;
      for (const f of remote.files) {
        this.syncedFileIds.add(f.file_id);
      }

      if (this.pending && this.conflictRetries < 2) {
        this.conflictRetries++;
        await this.runSave();
      } else if (this.conflictRetries >= 2) {
        this.setState("error", "canvasPanel.conflict");
      }
    } catch {
      this.setState("error", "canvasPanel.saveFailed");
    }
  }

  /** 缩略图异步生成(exportToCanvas 返回 Promise),由下一次保存捎带上送。 */
  private queueThumbnail() {
    if (
      !this.excalidrawAPI ||
      Date.now() - this.lastThumbnailAt < THUMBNAIL_INTERVAL_MS
    ) {
      return;
    }
    const generation = this.generation;
    this.lastThumbnailAt = Date.now();
    try {
      const elements = this.excalidrawAPI
        .getSceneElementsIncludingDeleted()
        .filter((el) => !el.isDeleted);
      if (!elements.length) {
        return;
      }
      const appState = this.excalidrawAPI.getAppState();
      Promise.resolve(
        exportToCanvas({
          elements: elements as never,
          appState: {
            exportBackground: true,
            viewBackgroundColor: appState.viewBackgroundColor,
          } as never,
          files: this.excalidrawAPI.getFiles(),
        }),
      )
        .then((canvas) => {
          const thumbnail = canvas.toDataURL("image/jpeg", 0.6);
          if (thumbnail.length < 61440 && generation === this.generation) {
            this.pendingThumbnail = thumbnail;
          }
        })
        .catch(() => {});
    } catch {
      // 缩略图失败不影响保存
    }
  }
}

export const canvasSaver = new CanvasSaver();
