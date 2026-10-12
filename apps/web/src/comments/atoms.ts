import { atom, appJotaiStore } from "../app-jotai";
import { canvasIdAtom } from "../canvas/atoms";

import type { CommentAnchor, CommentDraft, CommentThread } from "./types";

export const commentsRefreshAtom = atom(0);
export const commentSelectionAtom = atom<{
  threadId: string | null;
  anchor: CommentAnchor | null;
  placing: boolean;
  movingId: string | null;
  focus: boolean;
}>({
  threadId: null,
  anchor: null,
  placing: false,
  movingId: null,
  focus: false,
});
export const commentResolvedAtom = atom(false);
export const commentPinsAtom = atom<CommentThread[]>([]);
export const commentStatsAtom = atom({ total: 0, unread: 0 });
export const commentDraftsAtom = atom<Record<string, CommentDraft>>({});
export const refreshComments = (canvasId: string) => {
  if (appJotaiStore.get(canvasIdAtom) === canvasId) {
    appJotaiStore.set(commentsRefreshAtom, (version) => version + 1);
  }
};
export const selectComment = (threadId: string, focus = false) =>
  appJotaiStore.set(commentSelectionAtom, {
    threadId,
    anchor: null,
    placing: false,
    movingId: null,
    focus,
  });
