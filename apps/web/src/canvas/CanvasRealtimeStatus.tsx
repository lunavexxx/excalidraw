import { useI18n } from "@excalidraw/excalidraw/i18n";

import { useAtomValue } from "../app-jotai";

import { canvasRealtimeStatusAtom } from "./atoms";

export const CanvasRealtimeStatus = () => {
  const status = useAtomValue(canvasRealtimeStatusAtom);
  const { t } = useI18n();
  const key =
    status === "connected"
      ? "collabAccess.connected"
      : status === "reconnecting"
      ? "collabAccess.reconnecting"
      : "collabAccess.connecting";
  return (
    <span
      className="canvas-realtime-status"
      data-status={status}
      role="status"
      title={t("collabAccess.alwaysOnHint")}
    >
      {t(key)}
    </span>
  );
};
