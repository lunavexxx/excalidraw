import { useEffect, useRef, useState } from "react";
import { useExcalidrawAPI } from "@excalidraw/excalidraw";
import { useI18n } from "@excalidraw/excalidraw/i18n";

import { useAtom, useAtomValue, useSetAtom } from "../app-jotai";
import {
  canvasIdAtom,
  canvasRoleAtom,
  canvasTransitionAtom,
} from "../canvas/atoms";
import { currentUserAtom } from "../auth/atoms";

import {
  commentResolvedAtom,
  commentSelectionAtom,
  commentStatsAtom,
  commentsRefreshAtom,
  refreshComments,
  selectComment,
} from "./atoms";
import { commentApi, commentError, listCommentThreads } from "./api";
import { CommentAvatar } from "./CommentThreadView";

import type { CommentPage } from "./types";

export const CommentsPanel = () => {
  const { t } = useI18n();
  const api = useExcalidrawAPI();
  const canvasId = useAtomValue(canvasIdAtom);
  const role = useAtomValue(canvasRoleAtom);
  const transitioning = useAtomValue(canvasTransitionAtom);
  const user = useAtomValue(currentUserAtom);
  const revision = useAtomValue(commentsRefreshAtom);
  const selection = useAtomValue(commentSelectionAtom);
  const stats = useAtomValue(commentStatsAtom);
  const [resolved, setResolved] = useAtom(commentResolvedAtom);
  const setSelection = useSetAtom(commentSelectionAtom);
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState("date");
  const [page, setPage] = useState<CommentPage>({
    items: [],
    next_cursor: "",
    total: 0,
    unread: 0,
  });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const requestRef = useRef(0);
  const canWrite = !!user && role !== "guest";
  useEffect(() => {
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const timer = ownerWindow.setTimeout(() => setQuery(search), 200);
    return () => ownerWindow.clearTimeout(timer);
  }, [search]);
  useEffect(() => {
    setPage({ items: [], next_cursor: "", total: 0, unread: 0 });
    setSearch("");
    setQuery("");
  }, [canvasId]);
  useEffect(() => {
    const request = ++requestRef.current;
    if (!canvasId || transitioning) {
      return;
    }
    setLoading(true);
    setError("");
    void listCommentThreads(canvasId, { resolved, sort, search: query })
      .then((result) => {
        if (request === requestRef.current) {
          setPage(result);
        }
      })
      .catch((err) => {
        if (request === requestRef.current) {
          setError(commentError(err));
        }
      })
      .finally(() => {
        if (request === requestRef.current) {
          setLoading(false);
        }
      });
    return () => {
      requestRef.current = request + 1;
    };
  }, [
    canvasId,
    transitioning,
    role,
    user?.id,
    resolved,
    sort,
    query,
    revision,
  ]);
  return (
    <div className="CommentsPanel" ref={rootRef}>
      {!canvasId ? (
        <p className="CommentMuted">{t("comments.saveFirst")}</p>
      ) : (
        <>
          <div className="CommentsPanel__controls">
            <input
              type="search"
              aria-label={t("comments.search")}
              placeholder={t("comments.search")}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <div className="CommentsPanel__row">
              <select
                aria-label={t("comments.sort")}
                value={sort}
                onChange={(event) => setSort(event.target.value)}
              >
                <option value="date">{t("comments.sortDate")}</option>
                <option value="unread">{t("comments.sortUnread")}</option>
              </select>
              {canWrite && (
                <button
                  type="button"
                  className="CommentTextButton"
                  onClick={async () => {
                    try {
                      await commentApi(
                        canvasId,
                        "comment-threads/read-all",
                        "POST",
                      );
                      refreshComments(canvasId);
                    } catch (err) {
                      setError(commentError(err));
                    }
                  }}
                >
                  {t("comments.markAllRead")}
                </button>
              )}
            </div>
            <label className="CommentsPanel__row">
              <input
                type="checkbox"
                checked={resolved}
                onChange={(event) => setResolved(event.target.checked)}
              />
              {t("comments.showResolved")}
            </label>
            <div className="CommentsPanel__row">
              <span className="CommentMuted">
                {t("comments.unread", { count: stats.unread })}
              </span>
              {canWrite && (
                <button
                  type="button"
                  className="CommentPrimaryButton"
                  onClick={() => {
                    setSelection({
                      threadId: null,
                      anchor: null,
                      placing: true,
                      movingId: null,
                      focus: false,
                    });
                    api?.setActiveTool({ type: "selection" });
                    if (api && api.getAppState().width <= 730) {
                      api.updateScene({ appState: { openSidebar: null } });
                    }
                  }}
                >
                  {t("comments.new")}
                </button>
              )}
            </div>
          </div>
          {!canWrite && (
            <p className="CommentMuted">{t("comments.readOnly")}</p>
          )}
          <div className="CommentsPanel__list" aria-busy={loading}>
            {page.items.map((thread) => (
              <button
                type="button"
                className={`CommentsPanel__thread${
                  selection.threadId === thread.id ? " is-active" : ""
                }${thread.is_unread ? " is-unread" : ""}`}
                key={thread.id}
                onClick={() => selectComment(thread.id, true)}
              >
                <div className="CommentsPanel__row">
                  <CommentAvatar author={thread.author} />
                  <strong>
                    {thread.author.name || t("comments.unknownAuthor")}
                  </strong>
                  {thread.is_unread && <span className="CommentUnreadDot" />}
                </div>
                <p>
                  {thread.root_deleted ? t("comments.deleted") : thread.preview}
                </p>
                <div className="CommentsPanel__row CommentMuted">
                  <span>
                    {t("comments.replies", { count: thread.reply_count })}
                  </span>
                  {thread.resolved_at && <span>{t("comments.resolved")}</span>}
                  <time dateTime={thread.activity_at}>
                    {new Date(thread.activity_at).toLocaleDateString()}
                  </time>
                </div>
              </button>
            ))}
            {loading && <p role="status">{t("comments.loading")}</p>}
            {!loading && !page.items.length && !error && (
              <p className="CommentMuted">
                {t(query ? "comments.noResults" : "comments.empty")}
              </p>
            )}
            {page.next_cursor && (
              <button
                type="button"
                className="CommentTextButton"
                disabled={loading}
                onClick={async () => {
                  const request = requestRef.current;
                  setLoading(true);
                  try {
                    const result = await listCommentThreads(canvasId, {
                      resolved,
                      sort,
                      search: query,
                      cursor: page.next_cursor,
                    });
                    if (request === requestRef.current) {
                      setPage((previous) => ({
                        ...result,
                        items: [
                          ...new Map(
                            [...previous.items, ...result.items].map(
                              (thread) => [thread.id, thread],
                            ),
                          ).values(),
                        ],
                      }));
                    }
                  } catch (err) {
                    if (request === requestRef.current) {
                      setError(commentError(err));
                    }
                  } finally {
                    if (request === requestRef.current) {
                      setLoading(false);
                    }
                  }
                }}
              >
                {t("comments.loadMore")}
              </button>
            )}
          </div>
          {error && (
            <div>
              <p className="CommentError" role="alert">
                {error}
              </p>
              <button type="button" onClick={() => refreshComments(canvasId)}>
                {t("comments.retry")}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
};
