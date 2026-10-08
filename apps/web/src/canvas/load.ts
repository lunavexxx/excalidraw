import { CaptureUpdateAction } from "@excalidraw/excalidraw";
import { APP_NAME } from "@excalidraw/common";
import {
  restoreAppState,
  restoreElements,
} from "@excalidraw/excalidraw/data/restore";
import { getScrollToContentState } from "@excalidraw/excalidraw/viewport";
import { isInitializedImageElement } from "@excalidraw/element";

import type {
  ExcalidrawImperativeAPI,
  AppState,
  ExcalidrawInitialDataState,
} from "@excalidraw/excalidraw/types";
import type { ExcalidrawElement } from "@excalidraw/element/types";
import type { FileId } from "@excalidraw/element/types";

import { appJotaiStore } from "../app-jotai";
import { FileStatusStore } from "../data/fileStatusStore";
import { updateStaleImageStatuses } from "../data/FileManager";
import { clearGuestSession, getGuestToken } from "../auth/guestSession";
import { collabAPIAtom } from "../collab/Collab";
import { currentUserAtom } from "../auth/atoms";

import { canvasSaver } from "./saver";
import {
  canvasIdAtom,
  canvasRoleAtom,
  canvasCapabilitiesAtom,
  canvasTransitionAtom,
  type CanvasAccessRole,
} from "./atoms";
import { getCanvas, loadCanvasFiles } from "./api";
import { CanvasApiError } from "./api";

export const CANVAS_PATH_RE = /^\/c\/([0-9a-f-]{36})\/?$/;

export const parseCanvasIdFromPath = (): string | null => {
  const match = window.location.pathname.match(CANVAS_PATH_RE);
  return match ? match[1] : null;
};

export const isRootPath = () =>
  window.location.pathname.replace(/\/+$/, "") === "";

export type ServerCanvasResult = {
  scene: ExcalidrawInitialDataState;
  canvasId: string;
};

export type OpenServerCanvasOpts = {
  /** guest(分享链接)以 guest JWT 读取 */
  token?: string | null;
  /** guest 链接角色(editor/viewer);缺省从 my_role 推断 */
  role?: "editor" | "viewer";
};

// 协作与画布切换互斥(两套持久化);打开服务端画布自动进入实时房间。
const disconnectPreviousCanvas = (): Promise<void> => {
  const collab = appJotaiStore.get(collabAPIAtom);
  if (collab) {
    return collab.stopCollaboration();
  }
  return Promise.resolve();
};

// 有访问权限的用户打开服务端画布后自动加入；服务端角色决定可否编辑。
export const autoJoinCollab = (canvasId: string): Promise<void> => {
  const collab = appJotaiStore.get(collabAPIAtom);
  if (!collab) {
    return Promise.resolve();
  }
  const username = appJotaiStore.get(currentUserAtom)?.nickname;
  return collab
    .startCollaboration({ canvasId, username: username || undefined })
    .then(() => undefined)
    .catch((error) => {
      // 进房失败不阻断画布浏览(仍以静态快照展示)
      console.error("collab auto-join failed:", error);
    });
};

/**
 * 拉取服务端画布并 restore 成 initialData;成功后让 saver 以服务端
 * 版本为基准接管。appState 以服务端为准,仅沿用本地主题等 UI 偏好
 * (与协作场景同策略)。抛出 CanvasApiError 由调用方决定降级表现。
 * viewer/guest 打开:只读(viewModeEnabled),saver 静默。
 */
export const openServerCanvas = async (
  canvasId: string,
  localAppState: Partial<AppState> | null,
  excalidrawAPI: ExcalidrawImperativeAPI,
  opts?: OpenServerCanvasOpts,
): Promise<ServerCanvasResult> => {
  const token = opts?.token ?? getGuestToken(canvasId) ?? undefined;
  const detail = await getCanvas(canvasId, { token });
  const data = detail.scene?.data as
    | { elements?: unknown[]; appState?: Partial<AppState> }
    | undefined;

  const restoredAppState = restoreAppState(
    (data?.appState ?? null) as never,
    null,
  );
  // 先 restore 再 adopt:restore 抛错时不接管,调用方可安全重试
  const elements = restoreElements((data?.elements ?? []) as never, null, {
    repairBindings: true,
    deleteInvisibleElements: true,
  });

  const role: CanvasAccessRole = token ? "guest" : detail.my_role ?? "owner";
  appJotaiStore.set(canvasRoleAtom, role);
  appJotaiStore.set(
    canvasCapabilitiesAtom,
    detail.capabilities ?? {
      can_manage_collaborators: role === "owner",
      can_manage_share_links: role === "owner",
      can_review_requests: role === "owner",
    },
  );
  const readOnly = role === "viewer" || role === "guest";

  // 协议 v2:冷启动同步游标注入(协作进房后的 sync-request 以此为起点)
  appJotaiStore
    .get(collabAPIAtom)
    ?.setServerSeqCursor(canvasId, detail.scene?.cursor ?? 0);

  // 打开即接管:本地草稿不再作为该画布的持久层;viewer/guest 静默
  canvasSaver.init(excalidrawAPI);
  canvasSaver.adopt({
    canvasId,
    version: detail.scene?.version ?? 0,
    name: detail.canvas.name,
    fileIds: detail.files.map((f) => f.file_id),
    role: readOnly ? "viewer" : "editor",
  });

  return {
    canvasId,
    scene: {
      elements,
      appState: {
        ...restoredAppState,
        name: detail.canvas.name,
        theme: localAppState?.theme || restoredAppState.theme,
        // 只读访问(guest 链接 / viewer 协作者):禁编辑,保留平移缩放
        ...(readOnly ? { viewModeEnabled: true } : null),
      },
      scrollToContent: true,
    },
  };
};

/**
 * 服务端画布图片二进制:按场景引用的 fileId 从 canvas API 拉取并注入
 * 文件缓存,状态走 FileStatusStore(与首载 loadImages 同路径)。
 */
export const loadServerCanvasFiles = (
  canvasId: string,
  elements: readonly ExcalidrawElement[] | null | undefined,
  excalidrawAPI: ExcalidrawImperativeAPI,
) => {
  const fileIds =
    elements?.reduce((acc, element) => {
      if (isInitializedImageElement(element)) {
        return acc.concat(element.fileId);
      }
      return acc;
    }, [] as FileId[]) ?? [];

  if (!fileIds.length) {
    return;
  }
  FileStatusStore.updateStatuses(
    fileIds.map((fileId) => [fileId, "loading"] as [FileId, "loading"]),
  );
  loadCanvasFiles(canvasId, fileIds).then(({ loadedFiles, erroredFiles }) => {
    excalidrawAPI.addFiles(loadedFiles);
    updateStaleImageStatuses({
      excalidrawAPI,
      erroredFiles: erroredFiles as Map<FileId, true>,
      elements: excalidrawAPI.getSceneElementsIncludingDeleted(),
    });
    FileStatusStore.updateStatuses([
      ...loadedFiles.map((f) => [f.id, "loaded"] as [FileId, "loaded"]),
      ...[...erroredFiles.keys()].map(
        (fileId) => [fileId, "error"] as [FileId, "error"],
      ),
    ]);
  });
};

/**
 * 页内切换画布:flush 当前画布 → 拉取并 restore 新画布 → 原地换场景 →
 * 加载图片文件 → 更新 URL,全程不刷新页面。
 */
export const switchToCanvas = (params: {
  canvasId: string;
  excalidrawAPI: ExcalidrawImperativeAPI;
  /** popstate 回退时目标 URL 已在历史栈中,用 replace 防止重复条目 */
  historyMode?: "push" | "replace";
}): Promise<void> => {
  return enqueueCanvasSwitch(() => doSwitchToCanvas(params));
};

const doSwitchToCanvas = async (params: {
  canvasId: string;
  excalidrawAPI: ExcalidrawImperativeAPI;
  historyMode?: "push" | "replace";
}): Promise<void> => {
  const { canvasId, excalidrawAPI, historyMode = "push" } = params;
  if (appJotaiStore.get(canvasIdAtom) === canvasId) {
    return;
  }

  await disconnectPreviousCanvas();
  await canvasSaver.flushAsync();
  const prevAppState = excalidrawAPI.getAppState();
  const result = await openServerCanvas(canvasId, prevAppState, excalidrawAPI);
  const elements = result.scene.elements ?? [];

  // 先 adopt 后 resetScene:adopt 清 seenContent,resetScene 触发的空场景
  // onChange 会被 saver 的空快照兜底丢弃,不会 PUT 空数据
  excalidrawAPI.resetScene();
  const scroll = getScrollToContentState(elements, prevAppState);
  const restoredAppState = result.scene.appState as AppState;
  excalidrawAPI.updateScene({
    elements: elements as never,
    appState: {
      ...restoredAppState,
      openSidebar: prevAppState.openSidebar,
      scrollX: scroll.scrollX,
      scrollY: scroll.scrollY,
      // resetScene 保留当前 isLoading,显式复位(切换面板可能设过 true)
      isLoading: false,
    },
    captureUpdate: CaptureUpdateAction.NEVER,
  });
  loadServerCanvasFiles(canvasId, elements, excalidrawAPI);
  await autoJoinCollab(canvasId);

  const url = `/c/${canvasId}`;
  if (historyMode === "replace") {
    window.history.replaceState({}, APP_NAME, url);
  } else {
    window.history.pushState({}, APP_NAME, url);
  }
};

/** 页内回到空白草稿(新建画布/删除当前/回退到首页)。 */
export const switchToBlankCanvas = (params: {
  excalidrawAPI: ExcalidrawImperativeAPI;
  historyMode?: "push" | "replace";
}): Promise<void> => {
  return enqueueCanvasSwitch(() => doSwitchToBlankCanvas(params));
};

const doSwitchToBlankCanvas = async (params: {
  excalidrawAPI: ExcalidrawImperativeAPI;
  historyMode?: "push" | "replace";
}): Promise<void> => {
  const { excalidrawAPI, historyMode = "push" } = params;
  await disconnectPreviousCanvas();
  await canvasSaver.flushAsync();
  // resetScene 会把 openSidebar 重置为 null(收起侧边栏),先保留
  const openSidebar = excalidrawAPI.getAppState().openSidebar;
  // 离开服务端画布:重置内容角色并清理 guest 会话(如为分享链接访问)
  const prevCanvasId = appJotaiStore.get(canvasIdAtom);
  if (prevCanvasId) {
    clearGuestSession(prevCanvasId);
  }
  appJotaiStore.set(canvasRoleAtom, null);
  appJotaiStore.set(canvasCapabilitiesAtom, {
    can_manage_collaborators: false,
    can_manage_share_links: false,
    can_review_requests: false,
  });
  // detach 必须先于 resetScene:否则空场景 onChange 会以旧画布身份
  // 通过 seenContent 兜底,2s 后把空快照 PUT 上去清空云端画布
  canvasSaver.detach();
  excalidrawAPI.resetScene();
  excalidrawAPI.updateScene({
    appState: { openSidebar, isLoading: false, viewModeEnabled: false },
    captureUpdate: CaptureUpdateAction.NEVER,
  });

  if (historyMode === "replace") {
    window.history.replaceState({}, APP_NAME, "/");
  } else {
    window.history.pushState({}, APP_NAME, "/");
  }
};

/**
 * 页内切换串行队列:面板点击与 popstate 可能并发触发,串行保证
 * 场景/atom/URL 三者按触发顺序最终一致(最后一次切换生效)。
 */
let switchChain: Promise<void> = Promise.resolve();
const enqueueCanvasSwitch = (task: () => Promise<void>): Promise<void> => {
  const transition = async () => {
    appJotaiStore.set(canvasTransitionAtom, true);
    try {
      await task();
    } finally {
      appJotaiStore.set(canvasTransitionAtom, false);
    }
  };
  const run = switchChain.then(transition, transition);
  switchChain = run.catch(() => {});
  return run;
};

export { CanvasApiError };
