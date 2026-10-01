// 分享与协作对话框:
// - 实时协作:房间 = 画布本身(/c/:id),owner/editor 可发起;
// - 协作者与分享链接:信息类管理,一律 canvas owner 专属(服务端强制);
// - viewer/guest 只读,展示角色徽标。
import { trackEvent } from "@excalidraw/excalidraw/analytics";
import { copyTextToSystemClipboard } from "@excalidraw/excalidraw/clipboard";
import { Dialog } from "@excalidraw/excalidraw/components/Dialog";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";
import { TextField } from "@excalidraw/excalidraw/components/TextField";
import {
  copyIcon,
  LinkIcon,
  playerPlayIcon,
  playerStopFilledIcon,
  PlusIcon,
  TrashIcon,
} from "@excalidraw/excalidraw/components/icons";
import { useUIAppState } from "@excalidraw/excalidraw/context/ui-appState";
import { useCopyStatus } from "@excalidraw/excalidraw/hooks/useCopiedIndicator";
import { t, useI18n } from "@excalidraw/excalidraw/i18n";
import { KEYS, getFrame } from "@excalidraw/common";
import { useCallback, useEffect, useState } from "react";

import { atom, useAtom, useAtomValue } from "../app-jotai";
import { activeRoomLinkAtom } from "../collab/Collab";
import { canvasIdAtom, canvasRoleAtom } from "../canvas/atoms";
import { currentUserAtom } from "../auth/atoms";
import { canvasSaver } from "../canvas/saver";
import {
  createShareLink,
  listCollaborators,
  listShareLinks,
  putCollaborator,
  removeCollaborator,
  revokeShareLink,
} from "../canvas/api";
import { CanvasApiError } from "../canvas/api";

import "./ShareDialog.scss";

import type { CollaboratorPayload, ShareLinkPayload } from "../canvas/types";
import type { CollabAPI } from "../collab/Collab";

type OnExportToBackend = () => void;
type ShareDialogType = "share" | "collaborationOnly";

export const shareDialogStateAtom = atom<
  { isOpen: false } | { isOpen: true; type: ShareDialogType }
>({ isOpen: false });

export type ShareDialogProps = {
  collabAPI: CollabAPI | null;
  handleClose: () => void;
  onExportToBackend: OnExportToBackend;
  type: ShareDialogType;
};

const ActiveRoomDialog = ({
  collabAPI,
  activeRoomLink,
  handleClose,
}: {
  collabAPI: CollabAPI;
  activeRoomLink: string;
  handleClose: () => void;
}) => {
  const { t } = useI18n();
  const { onCopy, copyStatus } = useCopyStatus();

  return (
    <>
      <h3 className="ShareDialog__active__header">
        {t("sharePanel.liveTitle")}
      </h3>
      <TextField
        defaultValue={collabAPI.getUsername()}
        placeholder={t("sharePanel.yourName")}
        label={t("sharePanel.yourName")}
        onChange={collabAPI.setUsername}
        onKeyDown={(event) => event.key === KEYS.ENTER && handleClose()}
      />
      <div className="ShareDialog__active__linkRow">
        <TextField
          label={t("sharePanel.canvasLink")}
          readonly
          fullWidth
          value={activeRoomLink}
        />
        <FilledButton
          size="large"
          label={t("buttons.copyLink")}
          icon={copyIcon}
          status={copyStatus}
          onClick={async () => {
            try {
              await copyTextToSystemClipboard(activeRoomLink);
            } catch (e: any) {
              collabAPI.setCollabError(t("errors.copyToSystemClipboardFailed"));
            }
            onCopy();
          }}
        />
      </div>
      <div className="ShareDialog__active__description">
        <p>{t("sharePanel.liveHint")}</p>
      </div>

      <div className="ShareDialog__active__actions">
        <FilledButton
          size="large"
          variant="outlined"
          color="danger"
          label={t("sharePanel.stopSession")}
          icon={playerStopFilledIcon}
          onClick={() => {
            trackEvent("share", "room closed", `ui (${getFrame()})`);
            void collabAPI.stopCollaboration();
            handleClose();
          }}
        />
      </div>
    </>
  );
};

const ShareDialogPicker = (props: ShareDialogProps) => {
  const { t } = useI18n();
  const { collabAPI } = props;

  const canvasId = useAtomValue(canvasIdAtom);
  const role = useAtomValue(canvasRoleAtom);
  const currentUser = useAtomValue(currentUserAtom);
  const isOwner = role === "owner";
  const canEdit = role === "owner" || role === "editor";
  const isServerCanvas = !!canvasId;

  const startCollaboration = async () => {
    if (!collabAPI || !canvasId) {
      return;
    }
    trackEvent("share", "room creation", `ui (${getFrame()})`);
    // 确保画布已落库(协作房间标识 = canvasId)
    try {
      await canvasSaver.flushAsync();
    } catch {
      // flush 失败仍尝试:服务端 join ACL 会把未授权请求挡下
    }
    await collabAPI.startCollaboration({
      canvasId,
      username: currentUser?.nickname,
    });
    props.handleClose();
  };

  return (
    <>
      {collabAPI && isServerCanvas && canEdit && (
        <>
          <div className="ShareDialog__picker__header">
            {t("sharePanel.liveTitle")}
          </div>
          <div className="ShareDialog__picker__description">
            {t("sharePanel.liveIntro")}
          </div>
          <div className="ShareDialog__picker__button">
            <FilledButton
              size="large"
              label={t("sharePanel.startSession")}
              icon={playerPlayIcon}
              onClick={() => void startCollaboration()}
            />
          </div>
          <div className="ShareDialog__separator">
            <span>{t("sharePanel.or")}</span>
          </div>
        </>
      )}

      {collabAPI && isServerCanvas && role === "viewer" && (
        <div className="ShareDialog__picker__description">
          {t("sharePanel.viewerHint")}
        </div>
      )}

      {collabAPI && isServerCanvas && role === "guest" && (
        <div className="ShareDialog__picker__description">
          {t("sharePanel.guestHint")}
        </div>
      )}

      {!isServerCanvas && (
        <div className="ShareDialog__picker__description">
          {t("sharePanel.localDraftHint")}
        </div>
      )}

      {isOwner && canvasId && <CollaboratorSection canvasId={canvasId} />}

      {props.type === "share" && (
        <>
          <div className="ShareDialog__separator">
            <span>{t("sharePanel.or")}</span>
          </div>
          <div className="ShareDialog__picker__header">
            {t("exportDialog.link_title")}
          </div>
          <div className="ShareDialog__picker__description">
            {t("exportDialog.link_details")}
          </div>
          <div className="ShareDialog__picker__button">
            <FilledButton
              size="large"
              label={t("exportDialog.link_button")}
              icon={LinkIcon}
              onClick={async () => {
                await props.onExportToBackend();
                props.handleClose();
              }}
            />
          </div>
        </>
      )}
    </>
  );
};

const CollaboratorSection = ({ canvasId }: { canvasId: string }) => {
  const { t } = useI18n();
  const [collaborators, setCollaborators] = useState<
    CollaboratorPayload[] | null
  >(null);
  const [links, setLinks] = useState<ShareLinkPayload[] | null>(null);
  const [phone, setPhone] = useState("");
  const [inviteRole, setInviteRole] = useState<"editor" | "viewer">("editor");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const reload = useCallback(async () => {
    try {
      const [c, l] = await Promise.all([
        listCollaborators(canvasId),
        listShareLinks(canvasId),
      ]);
      setCollaborators(c);
      setLinks(l);
    } catch (err) {
      setError(
        err instanceof CanvasApiError
          ? err.message
          : t("canvasPanel.loadFailed"),
      );
    }
  }, [canvasId, t]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const invite = async () => {
    if (!phone.trim() || busy) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await putCollaborator(canvasId, phone.trim(), inviteRole);
      setPhone("");
      await reload();
    } catch (err) {
      setError(
        err instanceof CanvasApiError
          ? translatePermissionError(err.code)
          : t("canvasPanel.loadFailed"),
      );
    } finally {
      setBusy(false);
    }
  };

  const createLink = async (linkRole: "editor" | "viewer") => {
    setBusy(true);
    setError(null);
    try {
      const link = await createShareLink(canvasId, linkRole);
      const url = `${window.location.origin}/c/${canvasId}?share=${link.token}`;
      try {
        await copyTextToSystemClipboard(url);
      } catch {
        // 剪贴板失败时链接仍在弹层外可见?token 仅此一次——提示手动保存
      }
      await reload();
    } catch (err) {
      setError(
        err instanceof CanvasApiError
          ? translatePermissionError(err.code)
          : t("canvasPanel.loadFailed"),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="ShareDialog__separator">
        <span>{t("sharePanel.or")}</span>
      </div>

      <div className="ShareDialog__picker__header">
        {t("sharePanel.collaborators")}
      </div>
      <div className="ShareDialog__picker__description">
        {t("sharePanel.collaboratorsHint")}
      </div>
      <div className="ShareDialog__active__linkRow">
        <TextField
          value={phone}
          placeholder={t("sharePanel.phonePlaceholder")}
          label={t("sharePanel.phonePlaceholder")}
          onChange={setPhone}
          onKeyDown={(event) => event.key === KEYS.ENTER && void invite()}
        />
        <FilledButton
          size="large"
          label={t("sharePanel.roleEditor")}
          status={inviteRole === "editor" ? "success" : undefined}
          onClick={() => setInviteRole("editor")}
        />
        <FilledButton
          size="large"
          label={t("sharePanel.roleViewer")}
          status={inviteRole === "viewer" ? "success" : undefined}
          onClick={() => setInviteRole("viewer")}
        />
        <FilledButton
          size="large"
          label={t("sharePanel.invite")}
          icon={PlusIcon}
          disabled={busy || !phone.trim()}
          onClick={() => void invite()}
        />
      </div>
      {error && <div className="ShareDialog__picker__description">{error}</div>}
      {collaborators && collaborators.length > 0 && (
        <ul className="ShareDialog__collabList">
          {collaborators.map((c) => (
            <li key={c.user_id}>
              <span>
                {c.nickname}（{c.phone_masked}）
              </span>
              <span className="ShareDialog__collabList__role">
                {c.role === "editor"
                  ? t("sharePanel.roleEditor")
                  : t("sharePanel.roleViewer")}
              </span>
              <FilledButton
                size="medium"
                variant="icon"
                label={t("sharePanel.remove")}
                icon={TrashIcon}
                onClick={async () => {
                  try {
                    await removeCollaborator(canvasId, c.user_id);
                    await reload();
                  } catch {
                    /* 列表刷新时展示最新错误 */
                  }
                }}
              />
            </li>
          ))}
        </ul>
      )}

      <div className="ShareDialog__picker__header">{t("sharePanel.links")}</div>
      <div className="ShareDialog__picker__description">
        {t("sharePanel.linksHint")}
      </div>
      <div className="ShareDialog__active__linkRow">
        <FilledButton
          size="large"
          label={t("sharePanel.createEditorLink")}
          disabled={busy}
          onClick={() => void createLink("editor")}
        />
        <FilledButton
          size="large"
          label={t("sharePanel.createViewerLink")}
          disabled={busy}
          onClick={() => void createLink("viewer")}
        />
      </div>
      {links && links.length > 0 && (
        <ul className="ShareDialog__collabList">
          {links.map((l) => (
            <li key={l.id}>
              <span>
                {l.role === "editor"
                  ? t("sharePanel.roleEditor")
                  : t("sharePanel.roleViewer")}
                {" · "}
                {l.revoked_at
                  ? t("sharePanel.revoked")
                  : l.expires_at
                  ? `${t("sharePanel.expiresAt")} ${new Date(
                      l.expires_at,
                    ).toLocaleDateString()}`
                  : t("sharePanel.noExpiry")}
              </span>
              {!l.revoked_at && (
                <FilledButton
                  size="medium"
                  variant="icon"
                  label={t("sharePanel.revoke")}
                  icon={TrashIcon}
                  onClick={async () => {
                    try {
                      await revokeShareLink(canvasId, l.id);
                      await reload();
                    } catch {
                      /* 列表刷新时展示最新错误 */
                    }
                  }}
                />
              )}
            </li>
          ))}
        </ul>
      )}
    </>
  );
};

const translatePermissionError = (code: number): string => {
  switch (code) {
    case 42003:
      return t("sharePanel.errAlreadyMember");
    case 42004:
      return t("sharePanel.errLinkInvalid");
    case 42005:
      return t("sharePanel.errUserNotFound");
    case 42002:
      return t("sharePanel.errForbidden");
    default:
      return t("sharePanel.errGeneric");
  }
};

const ShareDialogInner = (props: ShareDialogProps) => {
  const activeRoomLink = useAtomValue(activeRoomLinkAtom);

  return (
    <Dialog size="small" onCloseRequest={props.handleClose} title={false}>
      <div className="ShareDialog">
        {props.collabAPI && activeRoomLink ? (
          <ActiveRoomDialog
            collabAPI={props.collabAPI}
            activeRoomLink={activeRoomLink}
            handleClose={props.handleClose}
          />
        ) : (
          <ShareDialogPicker {...props} />
        )}
      </div>
    </Dialog>
  );
};

export const ShareDialog = (props: {
  collabAPI: CollabAPI | null;
  onExportToBackend: OnExportToBackend;
}) => {
  const [shareDialogState, setShareDialogState] = useAtom(shareDialogStateAtom);

  const { openDialog } = useUIAppState();

  useEffect(() => {
    if (openDialog) {
      setShareDialogState({ isOpen: false });
    }
  }, [openDialog, setShareDialogState]);

  if (!shareDialogState.isOpen) {
    return null;
  }

  return (
    <ShareDialogInner
      handleClose={() => setShareDialogState({ isOpen: false })}
      collabAPI={props.collabAPI}
      onExportToBackend={props.onExportToBackend}
      type={shareDialogState.type}
    />
  );
};
