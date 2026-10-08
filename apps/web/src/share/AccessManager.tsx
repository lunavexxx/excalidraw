import { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { copyTextToSystemClipboard } from "@excalidraw/excalidraw/clipboard";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";

import { useAtomValue } from "../app-jotai";
import {
  canvasCapabilitiesAtom,
  canvasRoleAtom,
  collaborationRefreshAtom,
  onlineUsersAtom,
  presenceConnectedAtom,
} from "../canvas/atoms";
import {
  createShareLink,
  listShareLinks,
  putCollaborator,
  removeCollaborator,
  revokeShareLink,
} from "../canvas/api";

import {
  accessApi,
  accessEntries,
  createAccessEntry,
  invitations,
  inviteByPhone,
  members,
  requestAccess,
  requests,
} from "./accessApi";

import type {
  AccessEntry,
  AccessRequest,
  CanvasMember,
  Invitation,
  ShareLinkPayload,
} from "../canvas/types";

export const RequestList = ({
  items,
  canvasId,
  management,
  entry,
  onAction,
}: {
  items: AccessRequest[];
  canvasId: string;
  management: boolean;
  entry?: string;
  onAction: (work: () => Promise<unknown>) => void;
}) => {
  const { t } = useI18n();
  const [reviewReasons, setReviewReasons] = useState<Record<string, string>>(
    {},
  );
  return (
    <ul className="ShareDialog__collabList">
      {items.map((r) => (
        <li key={r.id}>
          <span>
            <strong>{r.nickname}</strong> · {t(`collabAccess.${r.role}`)}
            <small>{r.reason}</small>
            <small>
              {t(`collabAccess.${r.status}`)}
              {r.result_reason && ` · ${r.result_reason}`}
            </small>
          </span>
          {r.status === "pending" &&
            (management ? (
              <>
                <input
                  maxLength={1000}
                  aria-label={t("collabAccess.reviewReason")}
                  placeholder={t("collabAccess.reviewReason")}
                  value={reviewReasons[r.id] || ""}
                  onChange={(e) =>
                    setReviewReasons((p) => ({ ...p, [r.id]: e.target.value }))
                  }
                />
                <button
                  onClick={() =>
                    onAction(() =>
                      accessApi(
                        `/canvases/${canvasId}/access-requests/${r.id}/decision`,
                        "POST",
                        { decision: "approved", role: "viewer" },
                      ),
                    )
                  }
                >
                  {t("collabAccess.approveViewer")}
                </button>
                <button
                  onClick={() =>
                    onAction(() =>
                      accessApi(
                        `/canvases/${canvasId}/access-requests/${r.id}/decision`,
                        "POST",
                        { decision: "approved", role: "editor" },
                      ),
                    )
                  }
                >
                  {t("collabAccess.approveEditor")}
                </button>
                <button
                  onClick={() =>
                    onAction(() =>
                      accessApi(
                        `/canvases/${canvasId}/access-requests/${r.id}/decision`,
                        "POST",
                        {
                          decision: "rejected",
                          reason: reviewReasons[r.id] || "",
                        },
                      ),
                    )
                  }
                >
                  {t("collabAccess.reject")}
                </button>
              </>
            ) : (
              <button
                onClick={() =>
                  onAction(() =>
                    accessApi(
                      `/canvases/${canvasId}/access-requests/${r.id}`,
                      "DELETE",
                      undefined,
                      entry,
                    ),
                  )
                }
              >
                {t("collabAccess.cancel")}
              </button>
            ))}
        </li>
      ))}
    </ul>
  );
};

export const AccessManager = ({ canvasId }: { canvasId: string }) => {
  const { t } = useI18n();
  const rootRef = useRef<HTMLDivElement>(null);
  const caps = useAtomValue(canvasCapabilitiesAtom);
  const role = useAtomValue(canvasRoleAtom);
  const online = useAtomValue(onlineUsersAtom);
  const connected = useAtomValue(presenceConnectedAtom);
  const refreshVersion = useAtomValue(collaborationRefreshAtom);
  const manage = caps.can_manage_collaborators;
  const [people, setPeople] = useState<CanvasMember[]>([]);
  const [cursor, setCursor] = useState("");
  const [search, setSearch] = useState("");
  const [invites, setInvites] = useState<Invitation[]>([]);
  const [links, setLinks] = useState<ShareLinkPayload[]>([]);
  const [entries, setEntries] = useState<AccessEntry[]>([]);
  const [accessRequests, setRequests] = useState<AccessRequest[]>([]);
  const [phone, setPhone] = useState("");
  const [inviteRole, setInviteRole] = useState<"editor" | "viewer">("editor");
  const [reason, setReason] = useState("");
  const [link, setLink] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const reload = useCallback(async () => {
    const version = ++generation.current;
    try {
      const [m, r, extra] = await Promise.all([
        members(canvasId, "", search),
        requests(canvasId),
        manage
          ? Promise.all([
              invitations(canvasId),
              listShareLinks(canvasId),
              accessEntries(canvasId),
            ])
          : Promise.resolve(null),
      ]);
      if (version !== generation.current) {
        return;
      }
      setPeople(m.items);
      setCursor(m.next_cursor);
      setRequests(r.items);
      if (extra) {
        setInvites(extra[0].items);
        setLinks(extra[1]);
        setEntries(extra[2].items);
      }
    } catch {
      if (version === generation.current) {
        setError(t("collabAccess.failed"));
      }
    }
  }, [canvasId, manage, search, t]);
  useEffect(() => {
    void reload();
    return () => {
      generation.current = generation.current + 1;
    };
  }, [reload, refreshVersion]);
  const action = async (work: () => Promise<unknown>) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await work();
      await reload();
    } catch {
      setError(t("collabAccess.failed"));
    } finally {
      setBusy(false);
    }
  };
  const showLink = async (path: string) => {
    const ownerWindow = rootRef.current?.ownerDocument.defaultView;
    if (!ownerWindow) {
      return;
    }
    const url = new URL(path, ownerWindow.location.origin).href;
    setLink(url);
    try {
      await copyTextToSystemClipboard(url);
    } catch {
      setError(t("collabAccess.copyManually"));
    }
  };
  const onlineIds = new Set([...online.values()].map((u) => u.user_id));
  return (
    <div ref={rootRef} className="AccessManager" aria-busy={busy}>
      <h3>{t("collabAccess.members")}</h3>
      <p>{t("collabAccess.membersHint")}</p>
      <input
        aria-label={t("collabAccess.search")}
        placeholder={t("collabAccess.search")}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <ul className="ShareDialog__collabList">
        {people.map((m) => (
          <li key={m.user_id}>
            <span className="AccessManager__person">
              {m.avatar_url ? (
                <img src={m.avatar_url} alt="" />
              ) : (
                <span className="AccessManager__avatar">
                  {m.nickname.slice(0, 1)}
                </span>
              )}
              <span>
                {m.nickname}
                {m.phone_masked && <small>{m.phone_masked}</small>}
                <small>
                  {m.workspace_role && t("collabAccess.inherited")}
                  {m.can_manage && ` · ${t("collabAccess.manager")}`}
                </small>
              </span>
            </span>
            <span>
              {t(`collabAccess.${m.effective_role}`)}
              <small>
                {connected
                  ? t(
                      onlineIds.has(m.user_id)
                        ? "collabAccess.online"
                        : "collabAccess.offline",
                    )
                  : t("collabAccess.unknown")}
              </small>
            </span>
            {manage && m.direct_role && m.effective_role !== "owner" && (
              <>
                <select
                  aria-label={t("collabAccess.role")}
                  value={m.direct_role}
                  disabled={busy}
                  onChange={(e) =>
                    void action(() =>
                      accessApi(
                        `/canvases/${canvasId}/collaborators/${m.user_id}`,
                        "PATCH",
                        { role: e.target.value },
                      ),
                    )
                  }
                >
                  <option value="editor">{t("collabAccess.editor")}</option>
                  <option value="viewer">{t("collabAccess.viewer")}</option>
                </select>
                <button
                  disabled={busy}
                  onClick={() =>
                    void action(() => removeCollaborator(canvasId, m.user_id))
                  }
                >
                  {t("collabAccess.removeDirect")}
                </button>
              </>
            )}
          </li>
        ))}
      </ul>
      {cursor && (
        <button
          disabled={busy}
          onClick={() =>
            void (async () => {
              setBusy(true);
              try {
                const m = await members(canvasId, cursor, search);
                setPeople((p) => [...p, ...m.items]);
                setCursor(m.next_cursor);
              } catch {
                setError(t("collabAccess.failed"));
              } finally {
                setBusy(false);
              }
            })()
          }
        >
          {t("canvasPanel.loadMore")}
        </button>
      )}
      {manage && (
        <>
          <h3>{t("collabAccess.addInvite")}</h3>
          <p>{t("collabAccess.addHint")}</p>
          <div className="AccessManager__row">
            <input
              aria-label={t("sharePanel.phonePlaceholder")}
              placeholder={t("sharePanel.phonePlaceholder")}
              value={phone}
              onChange={(e) => setPhone(e.target.value)}
            />
            <select
              aria-label={t("collabAccess.role")}
              value={inviteRole}
              onChange={(e) =>
                setInviteRole(e.target.value as "editor" | "viewer")
              }
            >
              <option value="editor">{t("collabAccess.editor")}</option>
              <option value="viewer">{t("collabAccess.viewer")}</option>
            </select>
          </div>
          <div className="AccessManager__row">
            <FilledButton
              label={t("collabAccess.add")}
              disabled={busy || !phone.trim()}
              onClick={() =>
                void action(async () => {
                  await putCollaborator(canvasId, phone.trim(), inviteRole);
                  setPhone("");
                })
              }
            />
            <FilledButton
              label={t("collabAccess.inviteLink")}
              disabled={busy || !phone.trim()}
              onClick={() =>
                void action(async () => {
                  const i = await inviteByPhone(
                    canvasId,
                    phone.trim(),
                    inviteRole,
                  );
                  await showLink(`/invite?token=${i.token}`);
                  setPhone("");
                })
              }
            />
          </div>
          <h4>{t("collabAccess.invitations")}</h4>
          <ul className="ShareDialog__collabList">
            {invites.map((i) => (
              <li key={i.id}>
                <span>
                  {i.phone_masked} · {t(`collabAccess.${i.role}`)}
                  <small>
                    {t(
                      `collabAccess.${
                        i.status === "pending" &&
                        Date.parse(i.expires_at) < Date.now()
                          ? "expired"
                          : i.status
                      }`,
                    )}
                  </small>
                </span>
                {i.status === "pending" && (
                  <button
                    disabled={busy}
                    onClick={() =>
                      void action(() =>
                        accessApi(
                          `/canvases/${canvasId}/invitations/${i.id}`,
                          "DELETE",
                        ),
                      )
                    }
                  >
                    {t("collabAccess.revoke")}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h3>{t("collabAccess.shareLinks")}</h3>
          <p>{t("collabAccess.linkHint")}</p>
          <div className="AccessManager__row">
            {(["viewer", "editor"] as const).map((r) => (
              <FilledButton
                key={r}
                label={t(
                  r === "viewer"
                    ? "sharePanel.createViewerLink"
                    : "sharePanel.createEditorLink",
                )}
                disabled={busy}
                onClick={() =>
                  void action(async () => {
                    const l = await createShareLink(canvasId, r, 7);
                    await showLink(`/c/${canvasId}?share=${l.token}`);
                  })
                }
              />
            ))}
          </div>
          <ul className="ShareDialog__collabList">
            {links.map((l) => (
              <li key={l.id}>
                <span>
                  {t(`collabAccess.${l.role}`)}
                  <small>
                    {l.revoked_at
                      ? t("collabAccess.revoked")
                      : l.expires_at
                      ? new Date(l.expires_at).toLocaleString()
                      : t("sharePanel.noExpiry")}
                  </small>
                </span>
                {!l.revoked_at && (
                  <button
                    disabled={busy}
                    onClick={() =>
                      void action(() => revokeShareLink(canvasId, l.id))
                    }
                  >
                    {t("collabAccess.revoke")}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h3>{t("collabAccess.requestLinks")}</h3>
          <p>{t("collabAccess.requestLinkHint")}</p>
          <FilledButton
            label={t("collabAccess.createRequestLink")}
            disabled={busy}
            onClick={() =>
              void action(async () => {
                const e = await createAccessEntry(canvasId);
                await showLink(`/request-access?token=${e.token}`);
              })
            }
          />
          <ul className="ShareDialog__collabList">
            {entries.map((e) => (
              <li key={e.id}>
                <span>
                  {e.revoked_at
                    ? t("collabAccess.revoked")
                    : new Date(e.expires_at).toLocaleString()}
                </span>
                {!e.revoked_at && (
                  <button
                    disabled={busy}
                    onClick={() =>
                      void action(() =>
                        accessApi(
                          `/canvases/${canvasId}/access-entries/${e.id}`,
                          "DELETE",
                        ),
                      )
                    }
                  >
                    {t("collabAccess.revoke")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
      {(manage || role === "viewer") && (
        <>
          <h3>
            {t(manage ? "collabAccess.requests" : "collabAccess.myRequests")}
          </h3>
          <fieldset disabled={busy}>
            <RequestList
              items={accessRequests}
              canvasId={canvasId}
              management={manage}
              onAction={(w) => void action(w)}
            />
            {role === "viewer" &&
              !accessRequests.some((r) => r.status === "pending") && (
                <>
                  <textarea
                    maxLength={1000}
                    placeholder={t("collabAccess.reason")}
                    aria-label={t("collabAccess.reason")}
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                  />
                  <FilledButton
                    label={t("collabAccess.requestEditor")}
                    disabled={busy}
                    onClick={() =>
                      void action(() =>
                        requestAccess(canvasId, "editor", reason),
                      )
                    }
                  />
                </>
              )}
          </fieldset>
        </>
      )}
      {link && (
        <div className="AccessManager__createdLink">
          <label>
            {t("collabAccess.createdLink")}
            <input readOnly value={link} onFocus={(e) => e.target.select()} />
          </label>
          <button onClick={() => void action(() => showLink(link))}>
            {t("buttons.copyLink")}
          </button>
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </div>
  );
};
