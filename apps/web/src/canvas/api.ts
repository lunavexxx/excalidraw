import type { BinaryFileData, DataURL } from "@excalidraw/excalidraw/types";

import type { FileId } from "@excalidraw/element/types";

import { API_URL, fetchJson, refreshSession } from "../auth/api";
import { getAccessToken } from "../auth/tokens";

import type { ApiBody } from "../auth/types";

import type {
  CanvasDetail,
  CanvasErrorCode,
  CanvasFilePayload,
  CanvasMeta,
} from "./types";

export class CanvasApiError extends Error {
  code: CanvasErrorCode;

  constructor(code: CanvasErrorCode, message: string) {
    super(message);
    this.code = code;
  }
}

const assertOK = <T>(body: ApiBody): T => {
  if (body.code !== 0) {
    throw new CanvasApiError(body.code as CanvasErrorCode, body.message);
  }
  return body.data as T;
};

export const listCanvases = async (
  cursor?: string,
  limit = 20,
): Promise<{ items: CanvasMeta[]; next_cursor: string }> => {
  const query = new URLSearchParams({ limit: String(limit) });
  if (cursor) {
    query.set("cursor", cursor);
  }
  return assertOK(await fetchJson(`/canvases?${query}`, { method: "GET" }));
};

/** 打开默认页用:最近一次打开的画布(可能为空)。 */
export const getLastOpenedCanvas = async (): Promise<CanvasMeta | null> => {
  const { items } = await listCanvases(undefined, 1);
  return items[0] ?? null;
};

export const createCanvas = async (name?: string): Promise<CanvasMeta> => {
  const data = assertOK<{ canvas: CanvasMeta }>(
    await fetchJson("/canvases", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  );
  return data.canvas;
};

export const getCanvas = async (canvasId: string): Promise<CanvasDetail> =>
  assertOK(await fetchJson(`/canvases/${canvasId}`, { method: "GET" }));

export const saveCanvasScene = async (
  canvasId: string,
  data: { elements: unknown[]; appState: unknown },
  baseVersion: number,
  thumbnail?: string,
): Promise<{ version: number }> => {
  const payload: Record<string, unknown> = { data, base_version: baseVersion };
  if (thumbnail) {
    payload.thumbnail = thumbnail;
  }
  return assertOK(
    await fetchJson(`/canvases/${canvasId}/scene`, {
      method: "PUT",
      body: JSON.stringify(payload),
    }),
  );
};

export const renameCanvas = async (
  canvasId: string,
  name: string,
): Promise<CanvasMeta> => {
  const data = assertOK<{ canvas: CanvasMeta }>(
    await fetchJson(`/canvases/${canvasId}`, {
      method: "PATCH",
      body: JSON.stringify({ name }),
    }),
  );
  return data.canvas;
};

export const deleteCanvas = async (canvasId: string): Promise<void> => {
  assertOK(await fetchJson(`/canvases/${canvasId}`, { method: "DELETE" }));
};

export const putCanvasFiles = async (
  canvasId: string,
  files: CanvasFilePayload[],
): Promise<void> => {
  assertOK(
    await fetchJson(`/canvases/${canvasId}/files`, {
      method: "PUT",
      body: JSON.stringify({ files }),
    }),
  );
};

/** 文件内容是 apiresp 契约的唯一例外:成功是原始二进制,失败仍是 JSON 错误体。 */
const fetchCanvasFileBlob = async (
  canvasId: string,
  fileId: string,
): Promise<Blob> => {
  const doFetch = () =>
    fetch(`${API_URL}/canvases/${canvasId}/files/${fileId}`, {
      ...(getAccessToken()
        ? { headers: { Authorization: `Bearer ${getAccessToken()}` } }
        : null),
    });

  const isJson = (res: Response) =>
    (res.headers.get("content-type") ?? "").includes("application/json");

  let res = await doFetch();
  if (isJson(res)) {
    const body = await res.json();
    if (body.code !== 40100 || !(await refreshSession())) {
      throw new CanvasApiError(body.code as CanvasErrorCode, body.message);
    }
    res = await doFetch();
    if (isJson(res)) {
      const retryBody = await res.json();
      throw new CanvasApiError(
        retryBody.code as CanvasErrorCode,
        retryBody.message,
      );
    }
  }
  return res.blob();
};

const blobToDataURL = (blob: Blob): Promise<string> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () =>
      reject(reader.error ?? new Error("read blob failed"));
    reader.readAsDataURL(blob);
  });

/** 服务端拉取图片文件,返回 FileManager.loadFiles 同构的 {loaded, errored}。 */
export const loadCanvasFiles = async (
  canvasId: string,
  fileIds: string[],
): Promise<{
  loadedFiles: BinaryFileData[];
  erroredFiles: Map<string, true>;
}> => {
  const loadedFiles: BinaryFileData[] = [];
  const erroredFiles = new Map<string, true>();
  await Promise.all(
    fileIds.map(async (fileId) => {
      try {
        const blob = await fetchCanvasFileBlob(canvasId, fileId);
        loadedFiles.push({
          id: fileId as FileId,
          mimeType: (blob.type || "image/png") as BinaryFileData["mimeType"],
          dataURL: (await blobToDataURL(blob)) as DataURL,
          created: Date.now(),
          lastRetrieved: Date.now(),
        });
      } catch {
        erroredFiles.set(fileId, true);
      }
    }),
  );
  return { loadedFiles, erroredFiles };
};
