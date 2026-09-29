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

import { canvasSaver } from "./saver";
import { canvasIdAtom } from "./atoms";
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

/**
 * 拉取服务端画布并 restore 成 initialData;成功后让 saver 以服务端
 * 版本为基准接管。appState 以服务端为准,仅沿用本地主题等 UI 偏好
 * (与协作场景同策略)。抛出 CanvasApiError 由调用方决定降级表现。
 */
export const openServerCanvas = async (
  canvasId: string,
  localAppState: Partial<AppState> | null,
  excalidrawAPI: ExcalidrawImperativeAPI,
): Promise<ServerCanvasResult> => {
  const detail = await getCanvas(canvasId);
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
  // 打开即接管:本地草稿不再作为该画布的持久层
  canvasSaver.init(excalidrawAPI);
  canvasSaver.adopt({
    canvasId,
    version: detail.scene?.version ?? 0,
    name: detail.canvas.name,
    fileIds: detail.files.map((f) => f.file_id),
  });

  return {
    canvasId,
    scene: {
      elements,
      appState: {
        ...restoredAppState,
        name: detail.canvas.name,
        theme: localAppState?.theme || restoredAppState.theme,
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
  await canvasSaver.flushAsync();
  // resetScene 会把 openSidebar 重置为 null(收起侧边栏),先保留
  const openSidebar = excalidrawAPI.getAppState().openSidebar;
  // detach 必须先于 resetScene:否则空场景 onChange 会以旧画布身份
  // 通过 seenContent 兜底,2s 后把空快照 PUT 上去清空云端画布
  canvasSaver.detach();
  excalidrawAPI.resetScene();
  excalidrawAPI.updateScene({
    appState: { openSidebar, isLoading: false },
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
  const run = switchChain.then(task, task);
  switchChain = run.catch(() => {});
  return run;
};

export { CanvasApiError };
