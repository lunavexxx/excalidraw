import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  CaptureUpdateAction,
  useExcalidrawAPI,
  useExcalidrawStateValue,
} from "@excalidraw/excalidraw";
import {
  sceneCoordsToViewportCoords,
  viewportCoordsToSceneCoords,
} from "@excalidraw/common";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { messageCircleIcon } from "@excalidraw/excalidraw/components/icons";

import { appJotaiStore, useAtom, useAtomValue, useSetAtom } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";
import {
  canvasIdAtom,
  canvasRoleAtom,
  canvasTransitionAtom,
  canvasRealtimeStatusAtom,
} from "../canvas/atoms";

import {
  commentPinsAtom,
  commentResolvedAtom,
  commentSelectionAtom,
  commentStatsAtom,
  commentsRefreshAtom,
  refreshComments,
  selectComment,
} from "./atoms";
import {
  commentApi,
  commentError,
  getCommentThread,
  listCommentThreads,
} from "./api";
import { anchorPosition, createAnchor } from "./geometry";
import { CommentAvatar, CommentThreadView } from "./CommentThreadView";
import { CommentComposer } from "./CommentComposer";

import "./Comments.scss";

import type { CommentAnchor, CommentThread } from "./types";

const emptySelection = {
  threadId: null,
  anchor: null,
  placing: false,
  movingId: null,
  focus: false,
};

export const CommentModeButton = () => {
  const { t } = useI18n();
  const api = useExcalidrawAPI();
  const canvasId = useAtomValue(canvasIdAtom);
  const role = useAtomValue(canvasRoleAtom);
  const user = useAtomValue(currentUserAtom);
  const stats = useAtomValue(commentStatsAtom);
  const [selection, setSelection] = useAtom(commentSelectionAtom);
  return (
    <button
      type="button"
      className="CommentModeButton"
      aria-label={t("comments.title")}
      title={t("comments.title")}
      aria-pressed={selection.placing}
      onClick={() => {
        api?.updateScene({
          appState: {
            openSidebar:
              api.getAppState().width <= 730 &&
              canvasId &&
              user &&
              role !== "guest"
                ? null
                : { name: "default", tab: "comments" },
          },
          captureUpdate: CaptureUpdateAction.NEVER,
        });
        if (canvasId && user && role !== "guest") {
          setSelection({ ...emptySelection, placing: !selection.placing });
          api?.setActiveTool({ type: "selection" });
        }
      }}
    >
      {messageCircleIcon}
      {stats.unread > 0 && <span className="CommentUnreadDot" />}
    </button>
  );
};

export const CommentsController = () => {
  const { t } = useI18n();
  const api = useExcalidrawAPI();
  const canvasId = useAtomValue(canvasIdAtom);
  const role = useAtomValue(canvasRoleAtom);
  const user = useAtomValue(currentUserAtom);
  const transitioning = useAtomValue(canvasTransitionAtom);
  const realtime = useAtomValue(canvasRealtimeStatusAtom);
  const resolved = useAtomValue(commentResolvedAtom);
  const revision = useAtomValue(commentsRefreshAtom);
  const [selection, setSelection] = useAtom(commentSelectionAtom);
  const [pins, setPins] = useAtom(commentPinsAtom);
  const setStats = useSetAtom(commentStatsAtom);
  const state = useExcalidrawStateValue([
    "scrollX",
    "scrollY",
    "zoom",
    "offsetLeft",
    "offsetTop",
    "width",
    "height",
    "openSidebar",
    "defaultSidebarDockedPreference",
  ]);
  const markerRef = useRef<HTMLSpanElement>(null);
  const [container, setContainer] = useState<HTMLElement | null>(null);
  const [, setSceneRevision] = useState(0);
  const [viewport, setViewport] = useState({ bottom: 0, height: 0 });
  const [focusedThread, setFocusedThread] = useState<CommentThread | null>(
    null,
  );
  const [error, setError] = useState("");
  const downRef = useRef<{ x: number; y: number } | null>(null);
  const lastPositionsRef = useRef(
    new Map<string, { binding: string; x: number; y: number }>(),
  );
  const positionFor = useCallback(
    (id: string, anchor: CommentAnchor) => {
      const elements = api?.getSceneElementsIncludingDeleted() || [];
      const point = anchorPosition(anchor, elements);
      const binding = JSON.stringify([anchor.element_id, anchor.u, anchor.v]);
      const cached = lastPositionsRef.current.get(id);
      if (
        anchor.element_id &&
        !elements.some((element) => element.id === anchor.element_id) &&
        cached?.binding === binding
      ) {
        return { ...point, x: cached.x, y: cached.y };
      }
      lastPositionsRef.current.set(id, { binding, x: point.x, y: point.y });
      return point;
    },
    [api],
  );
  const canWrite = !!user && role !== "guest";
  useEffect(() => {
    setContainer(
      markerRef.current?.closest<HTMLElement>(".excalidraw") || null,
    );
  }, [api]);
  useEffect(() => {
    setSelection(emptySelection);
    setPins([]);
    setStats({ total: 0, unread: 0 });
    lastPositionsRef.current.clear();
    setFocusedThread(null);
    setError("");
  }, [canvasId, user?.id, setSelection, setPins, setStats]);
  useEffect(() => {
    if (!canWrite) {
      setSelection((previous) => ({
        ...previous,
        placing: false,
        anchor: null,
        movingId: null,
      }));
    }
  }, [canWrite, setSelection]);
  useEffect(() => {
    const visualViewport = container?.ownerDocument.defaultView?.visualViewport;
    if (!container || !visualViewport) {
      return;
    }
    const resize = () =>
      setViewport({
        bottom: Math.max(
          0,
          container.getBoundingClientRect().bottom -
            visualViewport.height -
            visualViewport.offsetTop,
        ),
        height: visualViewport.height,
      });
    resize();
    visualViewport.addEventListener("resize", resize);
    visualViewport.addEventListener("scroll", resize);
    return () => {
      visualViewport.removeEventListener("resize", resize);
      visualViewport.removeEventListener("scroll", resize);
    };
  }, [container]);
  useEffect(() => {
    if (!api || !container) {
      return;
    }
    const ownerWindow = container.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    let frame = 0;
    const repaint = () => {
      if (!frame) {
        frame = ownerWindow.requestAnimationFrame(() => {
          frame = 0;
          setSceneRevision((value) => value + 1);
        });
      }
    };
    const stop = api.onChange(repaint);
    const stopScroll = api.onScrollChange(repaint);
    return () => {
      stop();
      stopScroll();
      ownerWindow.cancelAnimationFrame(frame);
    };
  }, [api, container]);
  useEffect(() => {
    if (!canvasId || !container || transitioning) {
      return;
    }
    const ownerDocument = container.ownerDocument;
    const ownerWindow = ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const refresh = () => {
      if (!ownerDocument.hidden) {
        refreshComments(canvasId);
      }
    };
    const timer = ownerWindow.setInterval(refresh, 30000);
    ownerWindow.addEventListener("focus", refresh);
    ownerDocument.addEventListener("visibilitychange", refresh);
    return () => {
      ownerWindow.clearInterval(timer);
      ownerWindow.removeEventListener("focus", refresh);
      ownerDocument.removeEventListener("visibilitychange", refresh);
    };
  }, [canvasId, container, transitioning]);
  useEffect(() => {
    if (!canvasId || !container || transitioning) {
      return;
    }
    const value = new URLSearchParams(
      container.ownerDocument.defaultView?.location.search,
    ).get("comment");
    if (value && /^[0-9a-f-]{36}$/.test(value)) {
      selectComment(value, true);
    }
  }, [canvasId, container, transitioning]);
  useEffect(() => {
    if (!canvasId || transitioning) {
      return;
    }
    let disposed = false;
    const load = async () => {
      const items = new Map<string, CommentThread>();
      let cursor = "";
      do {
        const page = await listCommentThreads(
          canvasId,
          { resolved, cursor },
          true,
        );
        if (disposed) {
          return;
        }
        page.items.forEach((thread) => items.set(thread.id, thread));
        cursor = page.next_cursor;
        setStats({ total: page.total, unread: page.unread });
      } while (cursor);
      if (!disposed) {
        setPins([...items.values()]);
        setError("");
      }
    };
    void load().catch((err) => {
      if (!disposed) {
        setError(commentError(err));
      }
    });
    return () => {
      disposed = true;
    };
  }, [
    canvasId,
    transitioning,
    role,
    user?.id,
    resolved,
    revision,
    realtime,
    setPins,
    setStats,
  ]);
  useEffect(() => {
    if (
      !selection.focus ||
      !selection.threadId ||
      !canvasId ||
      !api ||
      !container
    ) {
      return;
    }
    let disposed = false;
    const focus = async () => {
      const thread =
        pins.find((item) => item.id === selection.threadId) ||
        (await getCommentThread(canvasId, selection.threadId!)).thread;
      if (disposed) {
        return;
      }
      setFocusedThread(thread);
      const point = positionFor(thread.id, thread.anchor);
      const appState = api.getAppState();
      const sidebarWidth =
        container.querySelector(".sidebar")?.getBoundingClientRect().width ||
        320;
      api.updateScene({
        appState: {
          scrollX:
            Math.max(100, appState.width - sidebarWidth) /
              (2 * appState.zoom.value) -
            point.x,
          scrollY: appState.height / (2 * appState.zoom.value) - point.y,
          openSidebar:
            appState.width <= 730 ? null : { name: "default", tab: "comments" },
        },
        captureUpdate: CaptureUpdateAction.NEVER,
      });
      setSelection((previous) => ({ ...previous, focus: false }));
    };
    void focus().catch((err) => {
      if (!disposed) {
        setError(commentError(err));
        setSelection((previous) => ({ ...previous, focus: false }));
      }
    });
    return () => {
      disposed = true;
    };
  }, [
    selection.focus,
    selection.threadId,
    canvasId,
    api,
    container,
    pins,
    positionFor,
    setSelection,
  ]);
  const close = () => {
    const id = selection.threadId;
    setSelection(emptySelection);
    const node = id
      ? container?.querySelector<HTMLButtonElement>(
          `[data-comment-pin="${id}"]`,
        )
      : container?.querySelector<HTMLButtonElement>(".CommentModeButton");
    node?.focus();
  };
  useEffect(() => {
    if (
      !container ||
      (!selection.threadId && !selection.anchor && !selection.placing)
    ) {
      return;
    }
    const ownerDocument = container.ownerDocument;
    const ownerWindow = ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const keydown = (event: KeyboardEvent) => {
      if (
        event.key === "Escape" &&
        !event.defaultPrevented &&
        !event.isComposing
      ) {
        event.preventDefault();
        setSelection(emptySelection);
        const node = selection.threadId
          ? container.querySelector<HTMLButtonElement>(
              `[data-comment-pin="${selection.threadId}"]`,
            )
          : container.querySelector<HTMLButtonElement>(".CommentModeButton");
        node?.focus();
      }
    };
    const outside = (event: PointerEvent) => {
      const target = event.target;
      if (
        target instanceof ownerWindow.Element &&
        !target.closest(
          ".CommentPopover,.CommentPin,.CommentsPanel,.CommentModeButton,.CommentPlacement",
        )
      ) {
        setSelection((previous) =>
          previous.placing ? previous : emptySelection,
        );
      }
    };
    ownerDocument.addEventListener("keydown", keydown);
    ownerDocument.addEventListener("pointerdown", outside);
    return () => {
      ownerDocument.removeEventListener("keydown", keydown);
      ownerDocument.removeEventListener("pointerdown", outside);
    };
  }, [
    container,
    selection.threadId,
    selection.anchor,
    selection.placing,
    setSelection,
  ]);
  if (!container || !api || !canvasId || transitioning || !state) {
    return <span hidden ref={markerRef} />;
  }
  const appState = api.getAppState();
  const bounds = container.getBoundingClientRect();
  const activeThread =
    pins.find((thread) => thread.id === selection.threadId) ||
    (focusedThread?.id === selection.threadId ? focusedThread : null);
  const anchor = selection.anchor || activeThread?.anchor;
  const point = anchor
    ? positionFor(selection.threadId || "draft", anchor)
    : { x: 0, y: 0, detached: false };
  const location = sceneCoordsToViewportCoords(
    { sceneX: point.x, sceneY: point.y },
    appState,
  );
  const sidebar = container.querySelector(".sidebar")?.getBoundingClientRect();
  const width =
    sidebar && appState.defaultSidebarDockedPreference
      ? sidebar.left - bounds.left
      : bounds.width;
  const popupWidth = Math.min(360, width - 16);
  const popupLeft = Math.max(
    8,
    Math.min(location.x - bounds.left + 22, width - popupWidth - 8),
  );
  const popupTop = Math.max(
    70,
    Math.min(location.y - bounds.top - 30, bounds.height - 370),
  );
  return (
    <>
      <span hidden ref={markerRef} />
      {createPortal(
        <div className="CommentLayer">
          {selection.placing && canWrite && (
            <div
              className="CommentPlacement"
              onPointerDown={(event) => {
                if (
                  event.button !== 0 ||
                  (!event.isPrimary && event.pointerType === "touch")
                ) {
                  return;
                }
                event.stopPropagation();
                downRef.current = { x: event.clientX, y: event.clientY };
              }}
              onPointerUp={async (event) => {
                event.stopPropagation();
                const down = downRef.current;
                downRef.current = null;
                if (
                  !down ||
                  Math.hypot(event.clientX - down.x, event.clientY - down.y) > 6
                ) {
                  return;
                }
                const nextAnchor = createAnchor(
                  viewportCoordsToSceneCoords(event, api.getAppState()),
                  api.getSceneElements(),
                  8 / api.getAppState().zoom.value,
                );
                if (selection.movingId) {
                  try {
                    const current = await getCommentThread(
                      canvasId,
                      selection.movingId,
                    );
                    await commentApi(
                      canvasId,
                      `comment-threads/${selection.movingId}`,
                      "PATCH",
                      { version: current.thread.version, anchor: nextAnchor },
                    );
                    if (appJotaiStore.get(canvasIdAtom) === canvasId) {
                      selectComment(selection.movingId);
                      refreshComments(canvasId);
                    }
                  } catch (err) {
                    if (appJotaiStore.get(canvasIdAtom) === canvasId) {
                      setError(commentError(err));
                    }
                  }
                } else {
                  setSelection({ ...emptySelection, anchor: nextAnchor });
                }
              }}
            >
              <div className="CommentPlacement__hint">
                {t(selection.movingId ? "comments.moving" : "comments.placing")}
                <button type="button" onClick={close}>
                  {t("comments.cancel")}
                </button>
              </div>
            </div>
          )}
          {pins.map((thread) => {
            const at = positionFor(thread.id, thread.anchor);
            const position = sceneCoordsToViewportCoords(
              { sceneX: at.x, sceneY: at.y },
              appState,
            );
            const left = position.x - bounds.left;
            const top = position.y - bounds.top;
            if (
              left < -30 ||
              left > width + 30 ||
              top < -30 ||
              top > bounds.height + 30
            ) {
              return null;
            }
            return (
              <button
                type="button"
                data-comment-pin={thread.id}
                className={`CommentPin${
                  selection.threadId === thread.id ? " is-active" : ""
                }${thread.resolved_at ? " is-resolved" : ""}`}
                key={thread.id}
                style={{ left, top }}
                aria-label={t("comments.pin", {
                  name: thread.author.name || t("comments.unknownAuthor"),
                })}
                title={
                  thread.root_deleted ? t("comments.deleted") : thread.preview
                }
                aria-pressed={selection.threadId === thread.id}
                onClick={() => {
                  if (appState.width <= 730) {
                    api.updateScene({
                      appState: { openSidebar: null },
                      captureUpdate: CaptureUpdateAction.NEVER,
                    });
                  }
                  selectComment(thread.id);
                }}
              >
                <CommentAvatar author={thread.author} />
                {thread.is_unread && <span className="CommentUnreadDot" />}
                {thread.reply_count > 0 && (
                  <span className="CommentPin__count">
                    {thread.reply_count > 99 ? "99+" : thread.reply_count}
                  </span>
                )}
              </button>
            );
          })}
          {(selection.threadId || selection.anchor) && (
            <div
              className="CommentPopover"
              style={
                {
                  left: popupLeft,
                  top: popupTop,
                  width: popupWidth,
                  ...(appState.width <= 730 && viewport.height
                    ? {
                        bottom: Math.max(76, viewport.bottom + 12),
                        "--comment-popup-max-height": `${Math.max(
                          160,
                          viewport.height - 108,
                        )}px`,
                      }
                    : {}),
                } as React.CSSProperties
              }
            >
              {selection.threadId ? (
                <CommentThreadView
                  key={`${canvasId}:${selection.threadId}`}
                  canvasId={canvasId}
                  threadId={selection.threadId}
                  onClose={close}
                  detached={point.detached}
                />
              ) : canWrite && selection.anchor ? (
                <div className="CommentThreadView">
                  <div className="CommentThreadView__header">
                    <strong>{t("comments.new")}</strong>
                    <button
                      type="button"
                      aria-label={t("comments.close")}
                      onClick={close}
                    >
                      ×
                    </button>
                  </div>
                  <div className="CommentThreadView__footer">
                    <CommentComposer
                      canvasId={canvasId}
                      draftKey="new"
                      onSubmit={async (draft) => {
                        const at = anchorPosition(
                          selection.anchor!,
                          api.getSceneElementsIncludingDeleted(),
                        );
                        const result = await commentApi<{ thread_id: string }>(
                          canvasId,
                          "comment-threads",
                          "POST",
                          {
                            body: draft.body,
                            mentions: draft.mentions,
                            request_id: draft.requestId,
                            anchor: { ...selection.anchor, x: at.x, y: at.y },
                          },
                        );
                        if (appJotaiStore.get(canvasIdAtom) === canvasId) {
                          selectComment(result.thread_id);
                          refreshComments(canvasId);
                        }
                      }}
                    />
                  </div>
                </div>
              ) : null}
            </div>
          )}
          {error && (
            <div className="CommentLayer__error">
              <span role="alert">{error}</span>
              <button type="button" onClick={() => refreshComments(canvasId)}>
                {t("comments.retry")}
              </button>
              <button
                type="button"
                aria-label={t("comments.close")}
                onClick={() => setError("")}
              >
                ×
              </button>
            </div>
          )}
        </div>,
        container,
      )}
    </>
  );
};
