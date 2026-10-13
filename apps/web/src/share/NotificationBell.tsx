import { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { bellIcon } from "@excalidraw/excalidraw/components/icons";

import { useAtomValue, appJotaiStore } from "../app-jotai";
import { clearGuestSession } from "../auth/guestSession";
import { currentUserAtom } from "../auth/atoms";
import { refreshSession } from "../auth/api";
import { getAccessToken } from "../auth/tokens";
import { refreshComments, selectComment } from "../comments/atoms";
import { collaborationRefreshAtom } from "../canvas/atoms";

import { shareDialogStateAtom } from "./ShareDialog";

import { accessApi, notifications } from "./accessApi";

import type { CollaborationNotification } from "../canvas/types";

export const NotificationBell = () => {
  const { t } = useI18n();
  const user = useAtomValue(currentUserAtom);
  const userId = user?.id;
  const version = useAtomValue(collaborationRefreshAtom);
  const rootRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<CollaborationNotification[]>([]);
  const [unread, setUnread] = useState(0);
  const [error, setError] = useState("");
  const [more, setMore] = useState(false);
  const requestRef = useRef(0);
  const reload = useCallback(async () => {
    if (!userId) {
      return;
    }
    const request = ++requestRef.current;
    try {
      const n = await notifications();
      if (request !== requestRef.current) {
        return;
      }
      setItems(n.items);
      setUnread(n.unread_count);
      setMore(n.items.length === 50);
      setError("");
    } catch {
      if (request === requestRef.current) {
        setError(t("collabAccess.failed"));
      }
    }
  }, [t, userId]);
  useEffect(() => {
    const requests = requestRef;
    setItems([]);
    setUnread(0);
    setMore(false);
    setOpen(false);
    return () => {
      requests.current++;
    };
  }, [userId]);
  useEffect(() => {
    void reload();
  }, [reload, version]);
  useEffect(() => {
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (
      ownerWindow &&
      new URLSearchParams(ownerWindow.location.search).has("manageAccess")
    ) {
      appJotaiStore.set(shareDialogStateAtom, { isOpen: true });
      ownerWindow.history.replaceState(null, "", ownerWindow.location.pathname);
    }
  }, []);
  useEffect(() => {
    if (!userId) {
      return;
    }
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    let disposed = false;
    let connection: import("socket.io-client").Socket | null = null;
    void import("socket.io-client").then(({ default: io }) => {
      if (disposed) {
        return;
      }
      connection = io(import.meta.env.VITE_APP_WS_SERVER_URL || "/", {
        transports: ["websocket"],
        auth: async (cb) => {
          try {
            await refreshSession();
            cb({ token: getAccessToken() });
          } catch {
            cb({});
          }
        },
      });
      connection.on("notifications-changed", () =>
        appJotaiStore.set(collaborationRefreshAtom, (n) => n + 1),
      );
      connection.on(
        "comment-reads-changed",
        ({ canvas_id }: { canvas_id: string }) => {
          refreshComments(canvas_id);
          appJotaiStore.set(collaborationRefreshAtom, (n) => n + 1);
        },
      );
      connection.on("connect", () => void reload());
      connection.on("disconnect", (reason) => {
        if (reason === "io server disconnect") {
          connection?.connect();
        }
      });
    });
    const timer = ownerWindow.setInterval(() => void reload(), 30000);
    return () => {
      disposed = true;
      connection?.close();
      ownerWindow.clearInterval(timer);
    };
  }, [userId, reload]);
  const label = `${t("collabAccess.notifications")}${
    unread > 0 ? ` (${unread})` : ""
  }`;
  return (
    <div className="CollaborationNotifications" ref={rootRef}>
      <button
        type="button"
        className="collaboration-share-button"
        title={label}
        aria-label={label}
        aria-expanded={open}
        onClick={() => {
          setOpen((v) => !v);
          void reload();
        }}
      >
        {bellIcon}
        {unread > 0 && (
          <span
            className="CollaborationNotifications__badge"
            aria-hidden="true"
          >
            {unread > 99 ? "99+" : unread}
          </span>
        )}
      </button>
      {open && (
        <div className="CollaborationNotifications__panel">
          <button
            onClick={async () => {
              try {
                await accessApi("/notifications/read", "POST", { id: 0 });
                await reload();
              } catch {
                setError(t("collabAccess.failed"));
              }
            }}
          >
            {t("collabAccess.markAllRead")}
          </button>
          {items.length === 0 && <p>{t("collabAccess.noNotifications")}</p>}
          {items.map((n) => (
            <button
              className={n.read_at ? "" : "is-unread"}
              key={n.id}
              onClick={async () => {
                try {
                  await accessApi("/notifications/read", "POST", { id: n.id });
                  await reload();
                  const ownerWindow =
                    rootRef.current?.ownerDocument.defaultView;
                  if (!ownerWindow) {
                    return;
                  }
                  if (
                    n.type === "member_added" ||
                    n.type === "access_approved" ||
                    n.type === "invitation_accepted"
                  ) {
                    clearGuestSession(n.canvas_id);
                  }
                  if (n.type.startsWith("comment_")) {
                    setOpen(false);
                    if (ownerWindow.location.pathname === `/c/${n.canvas_id}`) {
                      selectComment(n.entity_id, true);
                    } else {
                      ownerWindow.location.assign(
                        `/c/${n.canvas_id}?comment=${n.entity_id}`,
                      );
                    }
                  } else if (n.type === "invitation") {
                    ownerWindow.location.assign(`/invite?id=${n.entity_id}`);
                  } else if (n.type === "access_rejected") {
                    setOpen(false);
                  } else if (
                    ownerWindow.location.pathname === `/c/${n.canvas_id}`
                  ) {
                    setOpen(false);
                    appJotaiStore.set(shareDialogStateAtom, {
                      isOpen: true,
                    });
                  } else {
                    ownerWindow.location.assign(
                      `/c/${n.canvas_id}?manageAccess=1`,
                    );
                  }
                } catch {
                  setError(t("collabAccess.failed"));
                }
              }}
            >
              <strong>{n.canvas_name}</strong>
              <span>
                {n.actor_name && `${n.actor_name} · `}
                {t(`collabAccess.${n.type}`)}
              </span>
              <small>{new Date(n.created_at).toLocaleString()}</small>
            </button>
          ))}
          {more && (
            <button
              onClick={async () => {
                try {
                  const request = requestRef.current;
                  const n = await notifications(items[items.length - 1].id);
                  if (request !== requestRef.current) {
                    return;
                  }
                  setItems((p) => [...p, ...n.items]);
                  setMore(n.items.length === 50);
                } catch {
                  setError(t("collabAccess.failed"));
                }
              }}
            >
              {t("canvasPanel.loadMore")}
            </button>
          )}
          {error && <p role="alert">{error}</p>}
        </div>
      )}
    </div>
  );
};
