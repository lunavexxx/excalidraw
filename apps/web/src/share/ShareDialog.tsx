import { useRef } from "react";
import { copyTextToSystemClipboard } from "@excalidraw/excalidraw/clipboard";
import { Dialog } from "@excalidraw/excalidraw/components/Dialog";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";
import { useUIAppState } from "@excalidraw/excalidraw/context/ui-appState";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { useEffect, useState } from "react";

import { atom, useAtom, useAtomValue } from "../app-jotai";
import { canvasIdAtom, canvasRoleAtom } from "../canvas/atoms";
import { currentUserAtom } from "../auth/atoms";

import { CanvasRealtimeStatus } from "../canvas/CanvasRealtimeStatus";

import { AccessManager } from "./AccessManager";

import "./ShareDialog.scss";

type ShareDialogType = "share" | "collaborationOnly";
export const shareDialogStateAtom = atom<
  { isOpen: false } | { isOpen: true; type: ShareDialogType }
>({ isOpen: false });
export type ShareDialogProps = {
  handleClose: () => void;
  onExportToBackend: () => void;
  type: ShareDialogType;
};

const ShareDialogInner = (props: ShareDialogProps) => {
  const { t } = useI18n();
  const canvasId = useAtomValue(canvasIdAtom);
  const role = useAtomValue(canvasRoleAtom);
  const user = useAtomValue(currentUserAtom);
  const rootRef = useRef<HTMLDivElement>(null);
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
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
              <CanvasRealtimeStatus />
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
            </div>
            <p>{t("collabAccess.alwaysOnHint")}</p>
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

export const ShareDialog = (props: { onExportToBackend: () => void }) => {
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
