import { useRef } from "react";
import { copyTextToSystemClipboard } from "@excalidraw/excalidraw/clipboard";
import { Dialog } from "@excalidraw/excalidraw/components/Dialog";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";
import { useUIAppState } from "@excalidraw/excalidraw/context/ui-appState";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { useEffect, useState } from "react";

import { atom, useAtom, useAtomValue } from "../app-jotai";
import { activeRoomLinkAtom } from "../collab/Collab";
import {
  canvasIdAtom,
  canvasRoleAtom,
  presenceConnectedAtom,
} from "../canvas/atoms";
import { currentUserAtom } from "../auth/atoms";
import { canvasSaver } from "../canvas/saver";

import { AccessManager } from "./AccessManager";

import "./ShareDialog.scss";

import type { CollabAPI } from "../collab/Collab";

type ShareDialogType = "share" | "collaborationOnly";
export const shareDialogStateAtom = atom<
  { isOpen: false } | { isOpen: true; type: ShareDialogType }
>({ isOpen: false });
export type ShareDialogProps = {
  collabAPI: CollabAPI | null;
  handleClose: () => void;
  onExportToBackend: () => void;
  type: ShareDialogType;
};

const ShareDialogInner = (props: ShareDialogProps) => {
  const { t } = useI18n();
  const canvasId = useAtomValue(canvasIdAtom);
  const role = useAtomValue(canvasRoleAtom);
  const user = useAtomValue(currentUserAtom);
  const connected = useAtomValue(presenceConnectedAtom);
  const activeRoom = useAtomValue(activeRoomLinkAtom);
  const rootRef = useRef<HTMLDivElement>(null);
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      size="regular"
      onCloseRequest={props.handleClose}
      title={t("collabAccess.shareTitle")}
    >
      <div className="ShareDialog" ref={rootRef}>
        {canvasId ? (
          <>
            <div className="AccessManager__row">
              <span>
                {t(
                  connected
                    ? "collabAccess.connected"
                    : "collabAccess.notConnected",
                )}
              </span>
              <FilledButton
                label={t("buttons.copyLink")}
                onClick={async () => {
                  const ownerWindow =
                    rootRef.current?.ownerDocument.defaultView;
                  if (!ownerWindow) {
                    return;
                  }
                  const url = new URL(
                    `/c/${canvasId}`,
                    ownerWindow.location.origin,
                  ).href;
                  setAddress(url);
                  try {
                    await copyTextToSystemClipboard(url);
                  } catch {
                    setError(t("collabAccess.copyManually"));
                  }
                }}
              />
              {!activeRoom && props.collabAPI && (
                <FilledButton
                  label={t("sharePanel.startSession")}
                  disabled={busy}
                  onClick={async () => {
                    setBusy(true);
                    try {
                      await canvasSaver.flushAsync();
                      await props.collabAPI?.startCollaboration({
                        canvasId,
                        username: user?.nickname,
                      });
                    } catch {
                      setError(t("collabAccess.failed"));
                    } finally {
                      setBusy(false);
                    }
                  }}
                />
              )}
            </div>
            <p>{t("collabAccess.addressHint")}</p>
            {address && (
              <input
                aria-label={t("sharePanel.canvasLink")}
                readOnly
                value={address}
                onFocus={(e) => e.target.select()}
              />
            )}
            {role !== "guest" && user ? (
              <AccessManager key={canvasId} canvasId={canvasId} />
            ) : (
              <p>{t("sharePanel.guestHint")}</p>
            )}
          </>
        ) : (
          <p>{t("sharePanel.localDraftHint")}</p>
        )}
        {props.type === "share" && (
          <FilledButton
            label={t("exportDialog.link_button")}
            onClick={props.onExportToBackend}
          />
        )}
        {error && <p role="alert">{error}</p>}
      </div>
    </Dialog>
  );
};

export const ShareDialog = (props: {
  collabAPI: CollabAPI | null;
  onExportToBackend: () => void;
}) => {
  const [state, setState] = useAtom(shareDialogStateAtom);
  const { openDialog } = useUIAppState();
  useEffect(() => {
    if (openDialog) {
      setState({ isOpen: false });
    }
  }, [openDialog, setState]);
  return state.isOpen ? (
    <ShareDialogInner
      {...props}
      type={state.type}
      handleClose={() => setState({ isOpen: false })}
    />
  ) : null;
};
