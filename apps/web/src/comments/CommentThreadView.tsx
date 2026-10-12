import { useEffect, useRef, useState } from "react";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { share } from "@excalidraw/excalidraw/components/icons";

import { useAtomValue, useSetAtom } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";
import { canvasRoleAtom } from "../canvas/atoms";

import {
  commentApi,
  commentError,
  getCommentThread,
  CommentApiError,
} from "./api";
import {
  commentsRefreshAtom,
  commentSelectionAtom,
  refreshComments,
} from "./atoms";
import { CommentComposer } from "./CommentComposer";
import { EmojiPicker, QUICK_EMOJI } from "./EmojiPicker";

import type { CommentAuthor, CommentDetail, CommentMessage } from "./types";

export const CommentAvatar = ({ author }: { author: CommentAuthor }) => (
  <span className="CommentAvatar" aria-hidden="true">
    {author.avatar ? (
      <img src={author.avatar} alt="" />
    ) : (
      Array.from(author.name || "?")[0]
    )}
  </span>
);
const CommentBody = ({ message }: { message: CommentMessage }) => {
  const nodes: React.ReactNode[] = [];
  let offset = 0;
  for (const mention of message.mentions) {
    nodes.push(message.body.slice(offset, mention.start));
    nodes.push(
      <mark key={mention.start}>
        {message.body.slice(mention.start, mention.end)}
      </mark>,
    );
    offset = mention.end;
  }
  nodes.push(message.body.slice(offset));
  return <p className="CommentMessage__body">{nodes}</p>;
};

export const CommentThreadView = ({
  canvasId,
  threadId,
  onClose,
  detached = false,
}: {
  canvasId: string;
  threadId: string;
  onClose: () => void;
  detached?: boolean;
}) => {
  const { t } = useI18n();
  const user = useAtomValue(currentUserAtom);
  const role = useAtomValue(canvasRoleAtom);
  const canWrite = !!user && role !== "guest";
  const revision = useAtomValue(commentsRefreshAtom);
  const setSelection = useSetAtom(commentSelectionAtom);
  const rootRef = useRef<HTMLDivElement>(null);
  const [detail, setDetail] = useState<CommentDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteThread, setDeleteThread] = useState(false);
  const [reactionMessage, setReactionMessage] = useState<string | null>(null);
  const [participantKey, setParticipantKey] = useState<string | null>(null);
  const [copyFallback, setCopyFallback] = useState("");
  const [copied, setCopied] = useState(false);
  const requestRef = useRef(0);
  const readRef = useRef("");
  useEffect(() => {
    rootRef.current?.focus();
  }, []);
  useEffect(() => {
    const request = ++requestRef.current;
    setError("");
    void getCommentThread(canvasId, threadId)
      .then((result) => {
        if (request === requestRef.current) {
          setDetail(result);
        }
      })
      .catch((err) => {
        if (request === requestRef.current) {
          setError(commentError(err));
          if (
            err instanceof CommentApiError &&
            [41001, 43001].includes(err.code)
          ) {
            setDetail(null);
          }
        }
      });
    return () => {
      requestRef.current = request + 1;
    };
  }, [canvasId, threadId, revision]);
  useEffect(() => {
    if (!detail || !canWrite) {
      return;
    }
    const key = `${canvasId}:${threadId}:${detail.thread.version}`;
    if (readRef.current === key) {
      return;
    }
    readRef.current = key;
    const version = detail.thread.version;
    void commentApi(canvasId, `comment-threads/${threadId}/read`, "POST", {
      version,
      unread_version: detail.thread.unread_version,
    })
      .then(() => refreshComments(canvasId))
      .catch((err) => {
        readRef.current = "";
        setError(commentError(err));
      });
  }, [canvasId, threadId, detail, canWrite]);
  const endpoint = `comment-threads/${threadId}`;
  const action = async (operation: () => Promise<unknown>, refresh = true) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await operation();
      if (refresh) {
        refreshComments(canvasId);
      }
    } catch (err) {
      setError(commentError(err));
      if (err instanceof CommentApiError && err.code === 43002) {
        refreshComments(canvasId);
      }
    } finally {
      setBusy(false);
    }
  };
  const react = (message: CommentMessage, emoji: string) => {
    const own = message.reactions.some(
      (reaction) =>
        reaction.emoji === emoji &&
        reaction.users.some((person) => person.id === user?.id),
    );
    void action(() =>
      commentApi(
        canvasId,
        `${endpoint}/comments/${message.id}/reactions`,
        own ? "DELETE" : "PUT",
        { emoji },
      ),
    );
  };
  const thread = detail?.thread;
  return (
    <div
      className="CommentThreadView"
      ref={rootRef}
      role="dialog"
      aria-modal="false"
      tabIndex={-1}
      aria-label={t("comments.title")}
    >
      <div className="CommentThreadView__header">
        <strong>{t("comments.title")}</strong>
        {thread?.resolved_at && (
          <span className="CommentResolved">{t("comments.resolved")}</span>
        )}
        <div className="CommentThreadView__tools">
          {thread?.can_resolve && canWrite && (
            <button
              type="button"
              disabled={busy}
              onClick={() =>
                void action(() =>
                  commentApi(canvasId, endpoint, "PATCH", {
                    version: thread.version,
                    resolved: !thread.resolved_at,
                  }),
                )
              }
            >
              {t(thread.resolved_at ? "comments.reopen" : "comments.resolve")}
            </button>
          )}
          <button
            type="button"
            title={t("comments.copyLink")}
            aria-label={t("comments.copyLink")}
            onClick={async () => {
              const ownerWindow = rootRef.current?.ownerDocument.defaultView;
              if (!ownerWindow) {
                return;
              }
              const link = new URL(
                `/c/${canvasId}?comment=${threadId}`,
                ownerWindow.location.origin,
              ).href;
              try {
                await ownerWindow.navigator.clipboard.writeText(link);
                setCopied(true);
                setCopyFallback("");
              } catch {
                setCopyFallback(link);
              }
            }}
          >
            {share}
          </button>
          <button
            type="button"
            aria-label={t("comments.close")}
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </div>
      {copyFallback && (
        <div className="CommentThreadView__notice">
          <small>{t("comments.copyManually")}</small>
          <input
            aria-label={t("comments.copyLink")}
            value={copyFallback}
            readOnly
            onFocus={(event) => event.target.select()}
          />
        </div>
      )}
      {copied && (
        <small className="CommentThreadView__notice" role="status">
          {t("comments.copied")}
        </small>
      )}
      {detached && (
        <small className="CommentThreadView__notice">
          {t("comments.detached")}
        </small>
      )}
      <div className="CommentThreadView__messages">
        {!detail && !error && <p role="status">{t("comments.loading")}</p>}
        {!!detail?.next_before && (
          <button
            type="button"
            className="CommentTextButton"
            disabled={busy}
            onClick={() =>
              void action(async () => {
                const request = requestRef.current;
                const older = await getCommentThread(
                  canvasId,
                  threadId,
                  detail.next_before,
                );
                if (request === requestRef.current) {
                  if (older.thread.version !== detail.thread.version) {
                    refreshComments(canvasId);
                    return;
                  }
                  setDetail({
                    ...older,
                    comments: [
                      ...new Map(
                        [...older.comments, ...detail.comments].map(
                          (message) => [message.id, message],
                        ),
                      ).values(),
                    ].sort((a, b) => a.seq - b.seq),
                  });
                }
              }, false)
            }
          >
            {t("comments.older")}
          </button>
        )}
        {detail?.comments.map((message) => (
          <article className="CommentMessage" key={message.id}>
            <div className="CommentMessage__header">
              <CommentAvatar author={message.author} />
              <div>
                <strong>
                  {message.author.name || t("comments.unknownAuthor")}
                </strong>
                <time
                  dateTime={message.created_at}
                  title={new Date(message.created_at).toLocaleString()}
                >
                  {new Date(message.created_at).toLocaleString(undefined, {
                    month: "short",
                    day: "numeric",
                    hour: "2-digit",
                    minute: "2-digit",
                  })}
                </time>
                {message.edited_at && <small>{t("comments.edited")}</small>}
              </div>
              {!message.deleted_at &&
                canWrite &&
                (message.author.id === user?.id || thread?.can_delete) && (
                  <details className="CommentMessage__menu">
                    <summary aria-label={t("comments.messageActions")}>
                      ⋯
                    </summary>
                    {message.author.id === user?.id && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditingId(message.id);
                          setDeletingId(null);
                        }}
                      >
                        {t("comments.edit")}
                      </button>
                    )}
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => setDeletingId(message.id)}
                    >
                      {t("comments.delete")}
                    </button>
                  </details>
                )}
            </div>
            {message.deleted_at ? (
              <p className="CommentMuted">{t("comments.deleted")}</p>
            ) : editingId === message.id ? (
              <CommentComposer
                key={message.id}
                canvasId={canvasId}
                draftKey={`edit:${message.id}`}
                initialDraft={{
                  body: message.body,
                  mentions: message.mentions,
                  requestId: null,
                }}
                editing
                onCancel={() => setEditingId(null)}
                onSubmit={async (draft) => {
                  await commentApi(
                    canvasId,
                    `${endpoint}/comments/${message.id}`,
                    "PATCH",
                    {
                      body: draft.body,
                      mentions: draft.mentions,
                      version: thread?.version,
                    },
                  );
                  setEditingId(null);
                  refreshComments(canvasId);
                }}
              />
            ) : (
              <CommentBody message={message} />
            )}
            {deletingId === message.id && (
              <div className="CommentConfirm">
                <span>{t("comments.deleteCommentConfirm")}</span>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() =>
                    void action(async () => {
                      await commentApi(
                        canvasId,
                        `${endpoint}/comments/${message.id}`,
                        "DELETE",
                        { version: thread?.version },
                      );
                      setDeletingId(null);
                    })
                  }
                >
                  {t("comments.delete")}
                </button>
                <button type="button" onClick={() => setDeletingId(null)}>
                  {t("comments.cancel")}
                </button>
              </div>
            )}
            {!message.deleted_at && (
              <div className="CommentReactions">
                {message.reactions.map((reaction) => (
                  <span className="CommentReaction" key={reaction.emoji}>
                    <button
                      type="button"
                      className={
                        reaction.users.some((person) => person.id === user?.id)
                          ? "is-own"
                          : ""
                      }
                      aria-label={reaction.emoji}
                      aria-pressed={reaction.users.some(
                        (person) => person.id === user?.id,
                      )}
                      disabled={!canWrite || busy}
                      onClick={() => react(message, reaction.emoji)}
                    >
                      {reaction.emoji}
                    </button>
                    <button
                      type="button"
                      aria-label={`${t("comments.reactionUsers")}: ${
                        reaction.emoji
                      }`}
                      title={reaction.users
                        .map((person) => person.name)
                        .join(", ")}
                      onClick={() =>
                        setParticipantKey((key) =>
                          key === `${message.id}:${reaction.emoji}`
                            ? null
                            : `${message.id}:${reaction.emoji}`,
                        )
                      }
                    >
                      {reaction.users.length}
                    </button>
                    {participantKey === `${message.id}:${reaction.emoji}` && (
                      <div className="CommentReaction__participants">
                        {reaction.users.map((person) => (
                          <span key={person.id}>
                            <CommentAvatar author={person} />
                            {person.name}
                          </span>
                        ))}
                      </div>
                    )}
                  </span>
                ))}
                {canWrite && (
                  <button
                    type="button"
                    aria-label={t("comments.addReaction")}
                    aria-expanded={reactionMessage === message.id}
                    disabled={busy}
                    onClick={() =>
                      setReactionMessage((id) =>
                        id === message.id ? null : message.id,
                      )
                    }
                  >
                    ＋😊
                  </button>
                )}
                {reactionMessage === message.id && (
                  <div className="CommentReactionPicker">
                    <div className="CommentReactions__quick">
                      {QUICK_EMOJI.map((emoji) => (
                        <button
                          type="button"
                          key={emoji}
                          aria-label={emoji}
                          onClick={() => {
                            react(message, emoji);
                            setReactionMessage(null);
                          }}
                        >
                          {emoji}
                        </button>
                      ))}
                    </div>
                    <EmojiPicker
                      onSelect={(emoji) => react(message, emoji)}
                      onClose={() => setReactionMessage(null)}
                    />
                  </div>
                )}
              </div>
            )}
          </article>
        ))}
      </div>
      {error && (
        <div className="CommentThreadView__notice">
          <p className="CommentError" role="alert">
            {error}
          </p>
          <button type="button" onClick={() => refreshComments(canvasId)}>
            {t("comments.retry")}
          </button>
        </div>
      )}
      {thread && canWrite && (
        <div className="CommentThreadView__footer">
          {thread.resolved_at ? (
            <p className="CommentMuted">{t("comments.resolvedHint")}</p>
          ) : (
            <CommentComposer
              canvasId={canvasId}
              draftKey={`reply:${threadId}`}
              onSubmit={async (draft) => {
                await commentApi(canvasId, `${endpoint}/comments`, "POST", {
                  body: draft.body,
                  mentions: draft.mentions,
                  request_id: draft.requestId,
                });
                refreshComments(canvasId);
              }}
            />
          )}
          <div className="CommentThreadView__management">
            {thread.can_resolve && (
              <button
                type="button"
                className="CommentTextButton"
                onClick={() => {
                  setSelection({
                    threadId: null,
                    anchor: null,
                    placing: true,
                    movingId: threadId,
                    focus: false,
                  });
                }}
              >
                {t("comments.movePin")}
              </button>
            )}
            {thread.can_delete && (
              <button
                type="button"
                className="CommentTextButton"
                onClick={() => setDeleteThread(true)}
              >
                {t("comments.deleteThread")}
              </button>
            )}
          </div>
          {deleteThread && (
            <div className="CommentConfirm">
              <span>{t("comments.deleteThreadConfirm")}</span>
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  void action(async () => {
                    await commentApi(canvasId, endpoint, "DELETE", {
                      version: thread.version,
                    });
                    onClose();
                  })
                }
              >
                {t("comments.delete")}
              </button>
              <button type="button" onClick={() => setDeleteThread(false)}>
                {t("comments.cancel")}
              </button>
            </div>
          )}
        </div>
      )}
      {thread && !canWrite && (
        <p className="CommentThreadView__notice CommentMuted">
          {t("comments.readOnly")}
        </p>
      )}
    </div>
  );
};
