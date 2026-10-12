import { t } from "@excalidraw/excalidraw/i18n";

import { appJotaiStore } from "../app-jotai";
import { fetchJson, fetchJsonWithToken } from "../auth/api";
import {
  getGuestToken,
  getGuestShareToken,
  setGuestToken,
} from "../auth/guestSession";
import { canvasRoleAtom } from "../canvas/atoms";
import { exchangeShareToken } from "../canvas/api";

import type { CommentAuthor, CommentDetail, CommentPage } from "./types";

export class CommentApiError extends Error {
  constructor(public code: number) {
    super(String(code));
  }
}
export const commentError = (error: unknown) => {
  if (error instanceof CommentApiError) {
    if ([41001, 43001].includes(error.code)) {
      return t("comments.unavailable");
    }
    if (error.code === 43002) {
      return t("comments.conflict");
    }
    if (error.code === 43003) {
      return t("comments.rateLimit");
    }
    if ([40100, 42002].includes(error.code)) {
      return t("comments.readOnly");
    }
    if (error.code === 43000) {
      return t("comments.invalid");
    }
  }
  return t("comments.failed");
};
export const commentApi = async <T>(
  canvasId: string,
  suffix: string,
  method = "GET",
  payload?: unknown,
): Promise<T> => {
  const path = `/canvases/${canvasId}/${suffix}`;
  const init = {
    method,
    ...(payload === undefined ? {} : { body: JSON.stringify(payload) }),
  };
  const guest = appJotaiStore.get(canvasRoleAtom) === "guest";
  const token = guest ? getGuestToken(canvasId) : null;
  let body = token
    ? await fetchJsonWithToken(path, init, token)
    : await fetchJson(path, init);
  if (guest && body.code === 40100) {
    const shareToken = getGuestShareToken(canvasId);
    if (shareToken) {
      const session = await exchangeShareToken(canvasId, shareToken);
      setGuestToken(canvasId, session.access_token);
      body = await fetchJsonWithToken(path, init, session.access_token);
    }
  }
  if (body.code !== 0) {
    throw new CommentApiError(body.code);
  }
  return body.data as T;
};
export const listCommentThreads = (
  canvasId: string,
  options: {
    resolved: boolean;
    sort?: string;
    search?: string;
    cursor?: string;
  },
  pins = false,
) =>
  commentApi<CommentPage>(
    canvasId,
    `${pins ? "comment-pins" : "comment-threads"}?${new URLSearchParams({
      resolved: String(options.resolved),
      sort: options.sort || "date",
      search: options.search || "",
      cursor: options.cursor || "",
    })}`,
  );
export const getCommentThread = (
  canvasId: string,
  threadId: string,
  before = 0,
) =>
  commentApi<CommentDetail>(
    canvasId,
    `comment-threads/${threadId}?before=${before}`,
  );
export const mentionCandidates = (
  canvasId: string,
  search: string,
  cursor = "",
) =>
  commentApi<{ items: CommentAuthor[]; next_cursor: string }>(
    canvasId,
    `comment-mention-candidates?${new URLSearchParams({ search, cursor })}`,
  );
