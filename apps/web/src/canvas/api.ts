import type { BinaryFileData, DataURL } from "@excalidraw/excalidraw/types";

import type { FileId } from "@excalidraw/element/types";

import {
  API_URL,
  fetchJson,
  fetchJsonWithToken,
  refreshSession,
} from "../auth/api";
import { getAccessToken } from "../auth/tokens";

import type { ApiBody } from "../auth/types";

import type {
  CanvasDetail,
  CanvasErrorCode,
  CanvasFilePayload,
  CanvasMeta,
  CollaboratorPayload,
  GuestAccessPayload,
  ShareLinkPayload,
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
  scope: "mine" | "shared" = "mine",
): Promise<{ items: CanvasMeta[]; next_cursor: string }> => {
  const query = new URLSearchParams({ limit: String(limit), scope });
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

// opts.token:guest(分享链接)以 guest JWT 读取画布;登录用户省略。
export const getCanvas = async (
  canvasId: string,
  opts?: { token?: string | null },
): Promise<CanvasDetail> => {
  const init = { method: "GET" };
  const body =
    opts?.token != null
      ? await fetchJsonWithToken(`/canvases/${canvasId}`, init, opts.token)
      : await fetchJson(`/canvases/${canvasId}`, init);
  return assertOK(body);
};

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
  token?: string | null,
): Promise<Blob> => {
  const authHeader = token
    ? { Authorization: `Bearer ${token}` }
    : getAccessToken()
    ? { Authorization: `Bearer ${getAccessToken()}` }
    : null;
  const doFetch = () =>
    fetch(`${API_URL}/canvases/${canvasId}/files/${fileId}`, {
      ...(authHeader ? { headers: authHeader } : null),
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
  opts?: { token?: string | null },
): Promise<{
  loadedFiles: BinaryFileData[];
  erroredFiles: Map<FileId, true>;
}> => {
  const loadedFiles: BinaryFileData[] = [];
  const erroredFiles = new Map<FileId, true>();
  await Promise.all(
    fileIds.map(async (fileId) => {
      try {
        const blob = await fetchCanvasFileBlob(canvasId, fileId, opts?.token);
        loadedFiles.push({
          id: fileId as FileId,
          mimeType: (blob.type || "image/png") as BinaryFileData["mimeType"],
          dataURL: (await blobToDataURL(blob)) as DataURL,
          created: Date.now(),
          lastRetrieved: Date.now(),
        });
      } catch {
        erroredFiles.set(fileId as FileId, true);
      }
    }),
  );
  return { loadedFiles, erroredFiles };
};

// ---- 协作者 / 分享链接 / guest 换票(信息类管理一律 owner 专属)----

export const listCollaborators = async (
  canvasId: string,
): Promise<CollaboratorPayload[]> => {
  const data = assertOK<{ items: CollaboratorPayload[] }>(
    await fetchJson(`/canvases/${canvasId}/collaborators`, { method: "GET" }),
  );
  return data.items;
};

/** 按手机号邀请/变更协作者(owner;upsert 语义)。 */
export const putCollaborator = async (
  canvasId: string,
  phone: string,
  role: "editor" | "viewer",
): Promise<CollaboratorPayload> => {
  const data = assertOK<{ collaborator: CollaboratorPayload }>(
    await fetchJson(`/canvases/${canvasId}/collaborators`, {
      method: "PUT",
      body: JSON.stringify({ phone, role }),
    }),
  );
  return data.collaborator;
};

export const removeCollaborator = async (
  canvasId: string,
  userId: string,
): Promise<void> => {
  assertOK(
    await fetchJson(`/canvases/${canvasId}/collaborators/${userId}`, {
      method: "DELETE",
    }),
  );
};

export const listShareLinks = async (
  canvasId: string,
): Promise<ShareLinkPayload[]> => {
  const data = assertOK<{ items: ShareLinkPayload[] }>(
    await fetchJson(`/canvases/${canvasId}/share-links`, { method: "GET" }),
  );
  return data.items;
};

/** 创建分享链接;明文 token 仅在本次响应返回(前端拼 /c/:id?share=)。 */
export const createShareLink = async (
  canvasId: string,
  role: "editor" | "viewer",
  expiresInDays?: number,
): Promise<ShareLinkPayload> => {
  const data = assertOK<{ share_link: ShareLinkPayload }>(
    await fetchJson(`/canvases/${canvasId}/share-links`, {
      method: "POST",
      body: JSON.stringify({
        role,
        ...(expiresInDays ? { expires_in_days: expiresInDays } : null),
      }),
    }),
  );
  return data.share_link;
};

export const revokeShareLink = async (
  canvasId: string,
  linkId: string,
): Promise<void> => {
  assertOK(
    await fetchJson(`/canvases/${canvasId}/share-links/${linkId}`, {
      method: "DELETE",
    }),
  );
};

/** 匿名持 share token 换 guest JWT(唯一的无鉴权画布端点)。 */
export const exchangeShareToken = async (
  canvasId: string,
  shareToken: string,
): Promise<GuestAccessPayload> => {
  return assertOK(
    await fetch(`${API_URL}/canvases/${canvasId}/access`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ share_token: shareToken }),
    }).then((res) => res.json()),
  );
};
