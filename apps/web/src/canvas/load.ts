import {
  restoreAppState,
  restoreElements,
} from "@excalidraw/excalidraw/data/restore";

import type {
  ExcalidrawImperativeAPI,
  AppState,
  ExcalidrawInitialDataState,
} from "@excalidraw/excalidraw/types";

import { canvasSaver } from "./saver";
import { getCanvas } from "./api";
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
      elements: restoreElements((data?.elements ?? []) as never, null, {
        repairBindings: true,
        deleteInvisibleElements: true,
      }),
      appState: {
        ...restoredAppState,
        name: detail.canvas.name,
        theme: localAppState?.theme || restoredAppState.theme,
      },
      scrollToContent: true,
    },
  };
};

export { CanvasApiError };
