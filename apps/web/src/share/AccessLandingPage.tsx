import { useEffect, useRef, useState, useCallback } from "react";
import { useI18n, setLanguage, languages } from "@excalidraw/excalidraw/i18n";
import {
  EditorJotaiProvider,
  editorJotaiStore,
} from "@excalidraw/excalidraw/editor-jotai";

import { getPreferredLanguage } from "../app-language/language-detector";

import { Provider, appJotaiStore, useAtomValue } from "../app-jotai";
import { clearGuestSession } from "../auth/guestSession";
import { currentUserAtom } from "../auth/atoms";
import { collaborationRefreshAtom } from "../canvas/atoms";

import { refreshSession, logout } from "../auth/api";

import { NotificationBell } from "./NotificationBell";

import { accessApi, requestAccess, requests } from "./accessApi";
import { RequestList } from "./AccessManager";
import "./ShareDialog.scss";

import type { User } from "../auth/types";
import type { AccessRequest, Invitation } from "../canvas/types";

export const AccessLandingPage = ({
  kind,
  initialToken,
  invitationId,
}: {
  kind: "invite" | "request";
  initialToken: string;
  invitationId: string;
}) => {
  const { t } = useI18n();
  const refreshVersion = useAtomValue(collaborationRefreshAtom);
  const rootRef = useRef<HTMLDivElement>(null);
  const [token, setToken] = useState(initialToken);
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);
  const [invite, setInvite] = useState<Invitation | null>(null);
  const [canvas, setCanvas] = useState<{
    canvas_id: string;
    canvas_name: string;
  } | null>(null);
  const [items, setItems] = useState<AccessRequest[]>([]);
  const [role, setRole] = useState("viewer");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const ownerWindow = () => rootRef.current?.ownerDocument.defaultView;
  const reload = useCallback(async (id: string, entry: string) => {
    const r = await requests(id, entry);
    setItems(r.items);
  }, []);
  useEffect(() => {
    if (!canvas) {
      return;
    }
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const update = () =>
      void reload(canvas.canvas_id, token).catch(() =>
        setError(t("collabAccess.failed")),
      );
    update();
    const timer = ownerWindow.setInterval(update, 30000);
    return () => ownerWindow.clearInterval(timer);
  }, [canvas, token, refreshVersion, reload, t]);
  useEffect(() => {
    let disposed = false;
    const win = ownerWindow();
    if (!win) {
      return;
    }
    let entry = initialToken;
    try {
      if (entry) {
        win.sessionStorage.setItem(`collaboration-${kind}-token`, entry);
        win.history.replaceState(null, "", win.location.pathname);
      } else {
        entry = win.sessionStorage.getItem(`collaboration-${kind}-token`) || "";
      }
    } catch {
      /* retain the token in memory */
    }
    setToken(entry);
    void refreshSession()
      .then(async (account) => {
        if (disposed) {
          return;
        }
        setUser(account);
        appJotaiStore.set(currentUserAtom, account);
        if (account) {
          if (kind === "invite") {
            const i = await accessApi<Invitation>(
              invitationId
                ? `/invitations/${invitationId}`
                : "/invitations/preview",
              "GET",
              undefined,
              entry,
            );
            if (!disposed) {
              setInvite(i);
            }
          } else {
            const c = await accessApi<{
              canvas_id: string;
              canvas_name: string;
            }>("/access-entries/preview", "GET", undefined, entry);
            if (!disposed) {
              setCanvas(c);
              await reload(c.canvas_id, entry);
            }
          }
        }
      })
      .catch(() => {
        if (!disposed) {
          setError(t("collabAccess.failed"));
        }
      })
      .finally(() => {
        if (!disposed) {
          setReady(true);
        }
      });
    return () => {
      disposed = true;
    };
  }, [kind, initialToken, invitationId, t, reload]);
  const act = async (work: () => Promise<unknown>) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await work();
      if (canvas) {
        await reload(canvas.canvas_id, token);
      }
    } catch {
      setError(t("collabAccess.failed"));
    } finally {
      setBusy(false);
    }
  };
  const loginPath = (signup = false) => {
    const path = ownerWindow()?.location.pathname || "/";
    return `/${signup ? "signup" : "login"}?returnTo=${encodeURIComponent(
      `${path}${invitationId ? `?id=${invitationId}` : ""}`,
    )}`;
  };
  return (
    <div className="excalidraw CollaborationLanding" ref={rootRef}>
      <main className="AccessManager">
        <h2>
          {t(
            kind === "invite"
              ? "collabAccess.inviteTitle"
              : "collabAccess.requestAccess",
          )}
        </h2>
        {!ready ? (
          <p>{t("collabAccess.loading")}</p>
        ) : !user ? (
          <>
            <p>{t("collabAccess.loginToContinue")}</p>
            <a href={loginPath()}>{t("collabAccess.login")}</a>
            <a href={loginPath(true)}>{t("collabAccess.signup")}</a>
          </>
        ) : (
          <>
            <NotificationBell />
            <p>
              {user.nickname} ({user.phone_masked})
            </p>
            <button
              onClick={() =>
                void act(async () => {
                  await logout();
                  ownerWindow()?.location.assign(loginPath());
                })
              }
            >
              {t("collabAccess.switchAccount")}
            </button>
            {invite && (
              <>
                <h3>{invite.canvas_name}</h3>
                <p>
                  {invite.phone_masked} · {t(`collabAccess.${invite.role}`)}
                </p>
                <button
                  disabled={busy}
                  onClick={() =>
                    void act(async () => {
                      const result = await accessApi<{ canvas_id: string }>(
                        invitationId
                          ? `/invitations/${invitationId}/accept`
                          : "/invitations/accept",
                        "POST",
                        { token },
                      );
                      clearGuestSession(result.canvas_id);
                      ownerWindow()?.sessionStorage.removeItem(
                        "collaboration-invite-token",
                      );
                      ownerWindow()?.location.assign(`/c/${result.canvas_id}`);
                    })
                  }
                >
                  {t("collabAccess.accept")}
                </button>
              </>
            )}
            {canvas && (
              <>
                <h3>{canvas.canvas_name}</h3>
                <fieldset disabled={busy}>
                  <RequestList
                    items={items}
                    canvasId={canvas.canvas_id}
                    management={false}
                    entry={token}
                    onAction={(w) => void act(w)}
                  />
                  {!items.some((r) => r.status === "pending") && (
                    <>
                      <select
                        aria-label={t("collabAccess.role")}
                        value={role}
                        onChange={(e) => setRole(e.target.value)}
                      >
                        <option value="viewer">
                          {t("collabAccess.viewer")}
                        </option>
                        <option value="editor">
                          {t("collabAccess.editor")}
                        </option>
                      </select>
                      <textarea
                        maxLength={1000}
                        aria-label={t("collabAccess.reason")}
                        placeholder={t("collabAccess.reason")}
                        value={reason}
                        onChange={(e) => setReason(e.target.value)}
                      />
                      <button
                        onClick={() =>
                          void act(() =>
                            requestAccess(
                              canvas.canvas_id,
                              role,
                              reason,
                              token,
                            ),
                          )
                        }
                      >
                        {t("collabAccess.requestAccess")}
                      </button>
                    </>
                  )}
                  {items.some((r) => r.status === "approved") && (
                    <a
                      href={`/c/${canvas.canvas_id}`}
                      onClick={() => clearGuestSession(canvas.canvas_id)}
                    >
                      {canvas.canvas_name}
                    </a>
                  )}
                </fieldset>
              </>
            )}
          </>
        )}
        {error && <p role="alert">{error}</p>}
        <a href="/">{t("collabAccess.back")}</a>
      </main>
    </div>
  );
};

export const AccessLandingApp = (
  props: React.ComponentProps<typeof AccessLandingPage>,
) => {
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const lang =
      languages.find((l) => l.code === getPreferredLanguage()) || languages[0];
    void setLanguage(lang).then(() => setReady(true));
  }, []);
  return ready ? (
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <AccessLandingPage {...props} />
      </EditorJotaiProvider>
    </Provider>
  ) : null;
};
