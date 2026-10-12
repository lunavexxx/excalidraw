import { useEffect, useRef, useState } from "react";
import { useI18n } from "@excalidraw/excalidraw/i18n";

import { useAtom, useAtomValue } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";

import { commentDraftsAtom } from "./atoms";
import { commentError, mentionCandidates } from "./api";
import { EmojiPicker } from "./EmojiPicker";
import { updateDraftText } from "./geometry";

import type { CommentAuthor, CommentDraft } from "./types";

const emptyDraft: CommentDraft = { body: "", mentions: [], requestId: null };
export const CommentComposer = ({
  canvasId,
  draftKey,
  initialDraft,
  onSubmit,
  onCancel,
  editing = false,
}: {
  canvasId: string;
  draftKey: string;
  initialDraft?: CommentDraft;
  onSubmit: (draft: CommentDraft) => Promise<void>;
  onCancel?: () => void;
  editing?: boolean;
}) => {
  const { t } = useI18n();
  const user = useAtomValue(currentUserAtom);
  const key = `${user?.id}:${canvasId}:${draftKey}`;
  const [drafts, setDrafts] = useAtom(commentDraftsAtom);
  const draft = drafts[key] || initialDraft || emptyDraft;
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [caret, setCaret] = useState(0);
  const [people, setPeople] = useState<CommentAuthor[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [candidateIndex, setCandidateIndex] = useState(0);
  const composingRef = useRef(false);
  const candidateRequest = useRef(0);
  const range = useRef({ start: 0, end: 0 });
  const activeMention = /@([^@\n]{0,50})$/.exec(draft.body.slice(0, caret));
  const mentionStart = activeMention ? caret - activeMention[0].length : -1;
  const mentionSearch =
    activeMention &&
    !draft.mentions.some(
      (mention) => mention.start === mentionStart && caret <= mention.end,
    )
      ? activeMention[1]
      : null;
  useEffect(() => {
    textareaRef.current?.focus();
    if (!drafts[key] && initialDraft) {
      setDrafts((previous) => ({ ...previous, [key]: initialDraft }));
    }
    // Initialize once per composer; later edits live in the per-account draft.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  useEffect(() => {
    const request = ++candidateRequest.current;
    setPeople([]);
    setNextCursor("");
    setCandidateIndex(0);
    if (mentionSearch === null) {
      return;
    }
    const ownerWindow = textareaRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const timer = ownerWindow.setTimeout(() => {
      void mentionCandidates(canvasId, mentionSearch)
        .then((result) => {
          if (candidateRequest.current === request) {
            setPeople(result.items);
            setNextCursor(result.next_cursor);
          }
        })
        .catch(() => {
          if (candidateRequest.current === request) {
            setError(t("comments.failed"));
          }
        });
    }, 150);
    return () => {
      ownerWindow.clearTimeout(timer);
      candidateRequest.current = request + 1;
    };
  }, [canvasId, mentionSearch, t]);
  const save = (next: CommentDraft) => {
    setDrafts((previous) => ({ ...previous, [key]: next }));
  };
  const insert = (text: string, person?: CommentAuthor) => {
    const node = textareaRef.current;
    if (!node || busy) {
      return;
    }
    const start = person ? mentionStart : range.current.start;
    const end = person ? caret : range.current.end;
    const body = draft.body.slice(0, start) + text + draft.body.slice(end);
    const next = updateDraftText(draft, body);
    if (person) {
      next.mentions.push({
        user_id: person.id,
        start,
        end: start + text.trimEnd().length,
      });
      next.mentions.sort((a, b) => a.start - b.start);
    }
    save(next);
    const offset = start + text.length;
    setCaret(offset);
    range.current = { start: offset, end: offset };
    node.ownerDocument.defaultView?.requestAnimationFrame(() => {
      node.focus();
      node.setSelectionRange(offset, offset);
    });
  };
  const submit = async () => {
    const node = textareaRef.current;
    const ownerWindow = node?.ownerDocument.defaultView;
    if (
      !ownerWindow ||
      busy ||
      !draft.body.trim() ||
      Array.from(draft.body).length > 5000 ||
      composingRef.current
    ) {
      return;
    }
    const sent = {
      ...draft,
      requestId: draft.requestId || ownerWindow.crypto.randomUUID(),
    };
    save(sent);
    setBusy(true);
    setError("");
    try {
      await onSubmit(sent);
      setDrafts((previous) => {
        const next = { ...previous };
        delete next[key];
        return next;
      });
      setCaret(0);
      range.current = { start: 0, end: 0 };
    } catch (err) {
      setError(commentError(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="CommentComposer">
      <textarea
        ref={textareaRef}
        aria-label={t(
          editing ? "comments.editMessage" : "comments.replyPlaceholder",
        )}
        placeholder={t(
          editing ? "comments.editMessage" : "comments.replyPlaceholder",
        )}
        value={draft.body}
        disabled={busy}
        onCompositionStart={() => {
          composingRef.current = true;
        }}
        onCompositionEnd={() => {
          composingRef.current = false;
        }}
        onChange={(event) => {
          save(updateDraftText(draft, event.target.value));
          setError("");
          setCaret(event.target.selectionStart);
          range.current = {
            start: event.target.selectionStart,
            end: event.target.selectionEnd,
          };
        }}
        onSelect={(event) => {
          const node = event.currentTarget;
          setCaret(node.selectionStart);
          range.current = {
            start: node.selectionStart,
            end: node.selectionEnd,
          };
        }}
        onKeyDown={(event) => {
          if (
            composingRef.current ||
            event.nativeEvent.isComposing ||
            event.keyCode === 229
          ) {
            return;
          }
          if (people.length && ["ArrowDown", "ArrowUp"].includes(event.key)) {
            event.preventDefault();
            setCandidateIndex(
              (index) =>
                (index + (event.key === "ArrowDown" ? 1 : -1) + people.length) %
                people.length,
            );
          } else if (
            people.length &&
            event.key === "Enter" &&
            !event.shiftKey
          ) {
            event.preventDefault();
            insert(`@${people[candidateIndex].name} `, people[candidateIndex]);
          } else if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            void submit();
          } else if (event.key === "Escape" && people.length) {
            event.stopPropagation();
            setPeople([]);
          }
        }}
      />
      {!!people.length && (
        <div
          className="CommentComposer__mentions"
          role="listbox"
          aria-label={t("comments.mentionMembers")}
        >
          {people.map((person, index) => (
            <button
              type="button"
              role="option"
              aria-selected={index === candidateIndex}
              key={person.id}
              onPointerDown={(event) => event.preventDefault()}
              onClick={() => insert(`@${person.name} `, person)}
            >
              {person.name}
            </button>
          ))}
          {nextCursor && (
            <button
              type="button"
              onClick={() => {
                const request = candidateRequest.current;
                void mentionCandidates(
                  canvasId,
                  mentionSearch || "",
                  nextCursor,
                )
                  .then((result) => {
                    if (request === candidateRequest.current) {
                      setPeople((previous) => [...previous, ...result.items]);
                      setNextCursor(result.next_cursor);
                    }
                  })
                  .catch((err) => setError(commentError(err)));
              }}
            >
              {t("comments.loadMore")}
            </button>
          )}
        </div>
      )}
      <div className="CommentComposer__actions">
        <button
          type="button"
          aria-label={t("comments.emoji")}
          aria-expanded={emojiOpen}
          disabled={busy}
          onClick={() => setEmojiOpen((open) => !open)}
        >
          😊
        </button>
        <small>{Array.from(draft.body).length}/5000</small>
        {onCancel && (
          <button type="button" disabled={busy} onClick={onCancel}>
            {t("comments.cancel")}
          </button>
        )}
        <button
          type="button"
          className="CommentPrimaryButton"
          disabled={
            busy || !draft.body.trim() || Array.from(draft.body).length > 5000
          }
          onClick={() => void submit()}
        >
          {t(
            busy
              ? "comments.sending"
              : editing
              ? "comments.save"
              : "comments.send",
          )}
        </button>
      </div>
      {emojiOpen && (
        <EmojiPicker
          onSelect={(emoji) => insert(emoji)}
          onClose={() => {
            setEmojiOpen(false);
            textareaRef.current?.focus();
          }}
        />
      )}
      {error && (
        <p className="CommentError" role="alert">
          {error}
        </p>
      )}
    </div>
  );
};
