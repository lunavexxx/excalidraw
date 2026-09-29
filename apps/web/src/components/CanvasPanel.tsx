import React from "react";

import { CaptureUpdateAction } from "@excalidraw/excalidraw";
import { clearAppStateForLocalStorage } from "@excalidraw/excalidraw/appState";
import { useExcalidrawAPI } from "@excalidraw/excalidraw/components/App";
import { FilledButton } from "@excalidraw/excalidraw/components/FilledButton";
import { PlusIcon } from "@excalidraw/excalidraw/components/icons";
import { useI18n } from "@excalidraw/excalidraw/i18n";

import { useAtomValue } from "../app-jotai";
import { currentUserAtom } from "../auth/atoms";
import {
  canvasIdAtom,
  canvasSaveErrorAtom,
  canvasSaveStateAtom,
  draftDirtyAtom,
} from "../canvas/atoms";
import {
  createCanvas,
  deleteCanvas,
  listCanvases,
  putCanvasFiles,
  renameCanvas,
  saveCanvasScene,
} from "../canvas/api";
import {
  clearLocalDraft,
  localDraftNonEmpty,
  localDraftUpdatedAt,
} from "../canvas/localDraft";
import { canvasSaver, normalizeSentName } from "../canvas/saver";

import { LocalData } from "../data/LocalData";

import type { CanvasMeta } from "../canvas/types";

const PAGE_SIZE = 20;

const formatTime = (value: string | number) => {
  const date = new Date(value);
  const now = new Date();
  const sameDay = date.toDateString() === now.toDateString();
  if (sameDay) {
    return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return date.toLocaleDateString([], { month: "numeric", day: "numeric" });
};

export const CanvasPanel = () => {
  const { t } = useI18n();
  const excalidrawAPI = useExcalidrawAPI();
  const user = useAtomValue(currentUserAtom);
  const canvasId = useAtomValue(canvasIdAtom);
  const saveState = useAtomValue(canvasSaveStateAtom);
  const saveError = useAtomValue(canvasSaveErrorAtom);
  const draftDirty = useAtomValue(draftDirtyAtom);

  const [items, setItems] = React.useState<CanvasMeta[]>([]);
  const [nextCursor, setNextCursor] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const [importing, setImporting] = React.useState(false);
  const [renamingId, setRenamingId] = React.useState<string | null>(null);
  const [renameValue, setRenameValue] = React.useState("");
  const [confirmDeleteId, setConfirmDeleteId] = React.useState<string | null>(
    null,
  );
  const [newCanvasArmed, setNewCanvasArmed] = React.useState(false);
  const [discardArmed, setDiscardArmed] = React.useState(false);

  const refresh = React.useCallback(async (append = false, cursor = "") => {
    setLoading(true);
    try {
      const res = await listCanvases(cursor || undefined, PAGE_SIZE);
      setItems((prev) => (append ? [...prev, ...res.items] : res.items));
      setNextCursor(res.next_cursor);
    } catch {
      if (!append) {
        setItems([]);
        setNextCursor("");
      }
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    if (user) {
      void refresh();
    }
  }, [user, canvasId, refresh]);

  if (!user) {
    const draftTime = localDraftNonEmpty() ? localDraftUpdatedAt() : null;
    return (
      <div className="CanvasPanel" style={panelStyle}>
        <div style={listStyle}>
          {draftTime ? (
            <div style={draftCardStyle}>
              <span style={nameStyle}>{t("canvasPanel.localDraft")}</span>
              <span style={timeStyle}>
                {t("canvasPanel.draftLastEdited", {
                  time: formatTime(draftTime),
                })}
              </span>
            </div>
          ) : null}
          <div style={guideStyle}>
            <div style={guideTextStyle}>{t("canvasPanel.loginPrompt")}</div>
            <FilledButton
              label={t("canvasPanel.loginAction")}
              onClick={() => {
                window.location.assign("/login");
              }}
            />
          </div>
        </div>
        <div style={footerStyle}>
          <div style={dividerStyle} />
          <FilledButton
            size="medium"
            fullWidth
            icon={PlusIcon}
            color={newCanvasArmed ? "warning" : "primary"}
            onClick={() => void handleNewCanvas()}
          >
            {newCanvasArmed
              ? t("canvasPanel.newCanvasConfirm")
              : t("canvasPanel.newCanvas")}
          </FilledButton>
        </div>
      </div>
    );
  }

  const hasSceneContent = () => {
    return !!excalidrawAPI
      ?.getSceneElementsIncludingDeleted()
      .some((el) => !el.isDeleted);
  };

  const resetToBlankDraft = () => {
    if (!excalidrawAPI) {
      return;
    }
    excalidrawAPI.resetScene();
    canvasSaver.detach();
    window.history.replaceState({}, document.title, "/");
  };

  const handleNewCanvas = async () => {
    if (!excalidrawAPI) {
      return;
    }
    if (!user) {
      // 匿名:场景有内容时重置会清空本地草稿,先确认
      if (hasSceneContent() && !newCanvasArmed) {
        setNewCanvasArmed(true);
        return;
      }
      setNewCanvasArmed(false);
      resetToBlankDraft();
      return;
    }
    // 登录态:先落库待保存改动,避免 detach 丢弃 2s 防抖窗口内的内容
    await canvasSaver.flushAsync();
    setNewCanvasArmed(false);
    resetToBlankDraft();
    void refresh();
  };

  const handleDiscardLocal = () => {
    if (!excalidrawAPI || !localDraftNonEmpty()) {
      setDiscardArmed(false);
      return;
    }
    if (!discardArmed) {
      setDiscardArmed(true);
      return;
    }
    setDiscardArmed(false);
    LocalData.flushSave();
    clearLocalDraft();
    excalidrawAPI.resetScene();
  };

  const handleImportLocal = async () => {
    if (!excalidrawAPI || importing) {
      return;
    }
    setImporting(true);
    try {
      // 先 flush 本地防抖(登录态为 files-only),防陈旧参数稍后复活草稿
      LocalData.flushSave();
      const elements =
        excalidrawAPI.getSceneElementsIncludingDeleted() as unknown[];
      const appState = clearAppStateForLocalStorage(
        excalidrawAPI.getAppState(),
      );
      const files = excalidrawAPI.getFiles();
      const canvas = await createCanvas(
        normalizeSentName(excalidrawAPI.getName()),
      );
      const payloads = Object.entries(files)
        .map(([fileId, file]) => {
          const dataURL: string = (file as any).dataURL ?? "";
          const base64 = dataURL.split(",")[1];
          if (!base64) {
            return null;
          }
          return {
            file_id: fileId,
            mime_type: (file as any).mimeType ?? "image/png",
            data: base64,
          };
        })
        .filter(Boolean) as {
        file_id: string;
        mime_type: string;
        data: string;
      }[];
      if (payloads.length) {
        await putCanvasFiles(canvas.id, payloads);
      }
      const { version } = await saveCanvasScene(
        canvas.id,
        { elements, appState },
        0,
      );
      clearLocalDraft();
      canvasSaver.adopt({
        canvasId: canvas.id,
        version,
        name: canvas.name,
        fileIds: payloads.map((f) => f.file_id),
      });
      window.history.replaceState({}, document.title, `/c/${canvas.id}`);
      void refresh();
    } catch {
      // 失败保持本地草稿不动,下次可重试
    } finally {
      setImporting(false);
    }
  };

  const handleRenameCommit = async (canvas: CanvasMeta) => {
    const name = renameValue.trim();
    setRenamingId(null);
    if (!name || name === canvas.name) {
      return;
    }
    try {
      await renameCanvas(canvas.id, name);
      if (canvas.id === canvasId && excalidrawAPI) {
        excalidrawAPI.updateScene({
          appState: { name },
          captureUpdate: CaptureUpdateAction.NEVER,
        });
        canvasSaver.noteServerName(name);
      }
      setItems((prev) =>
        prev.map((item) => (item.id === canvas.id ? { ...item, name } : item)),
      );
    } catch {
      // 重命名失败保持原名
    }
  };

  const handleDelete = async (canvas: CanvasMeta) => {
    if (confirmDeleteId !== canvas.id) {
      setConfirmDeleteId(canvas.id);
      return;
    }
    setConfirmDeleteId(null);
    try {
      await deleteCanvas(canvas.id);
      if (canvas.id === canvasId) {
        // 删除当前画布:回首页空白草稿
        excalidrawAPI?.resetScene();
        canvasSaver.detach();
        window.history.replaceState({}, document.title, "/");
      }
      setItems((prev) => prev.filter((item) => item.id !== canvas.id));
    } catch {
      // 删除失败保留条目
    }
  };

  return (
    <div className="CanvasPanel" style={panelStyle}>
      <div style={listStyle}>
        {!canvasId && localDraftNonEmpty() ? (
          <div style={draftCardStyle}>
            <div style={draftCardHeadStyle}>
              <span style={nameStyle}>{t("canvasPanel.localDraft")}</span>
              {localDraftUpdatedAt() ? (
                <span style={timeStyle}>
                  {t("canvasPanel.draftLastEdited", {
                    time: formatTime(localDraftUpdatedAt()!),
                  })}
                </span>
              ) : null}
            </div>
            <div style={draftCardActionsStyle}>
              <FilledButton
                variant="outlined"
                color="primary"
                label={
                  importing
                    ? t("canvasPanel.importing")
                    : t("canvasPanel.importLocal")
                }
                onClick={() => void handleImportLocal()}
                disabled={importing}
              />
              <button
                style={{
                  ...actionBtnStyle,
                  ...(discardArmed ? deleteArmedStyle : {}),
                }}
                onClick={handleDiscardLocal}
              >
                {discardArmed
                  ? t("canvasPanel.discardConfirm")
                  : t("canvasPanel.discardLocal")}
              </button>
            </div>
          </div>
        ) : null}

        {!canvasId && draftDirty ? (
          <div style={unsavedStyle}>{t("canvasPanel.unsavedDraft")}</div>
        ) : null}

        {items.length === 0 && !loading ? (
          <div style={emptyStyle}>{t("canvasPanel.empty")}</div>
        ) : null}

        {items.map((canvas) => (
          <div
            key={canvas.id}
            style={{
              ...itemStyle,
              borderColor:
                canvas.id === canvasId ? "var(--color-primary)" : "transparent",
            }}
          >
            <div
              style={itemMainStyle}
              onClick={() => {
                if (canvas.id !== canvasId) {
                  window.location.assign(`/c/${canvas.id}`);
                }
              }}
            >
              {canvas.thumbnail ? (
                <img
                  src={canvas.thumbnail}
                  alt=""
                  style={thumbStyle}
                  draggable={false}
                />
              ) : (
                <div style={{ ...thumbStyle, ...thumbEmptyStyle }}>
                  {t("canvasPanel.untitled").charAt(0)}
                </div>
              )}
              <div style={itemMetaStyle}>
                {renamingId === canvas.id ? (
                  <input
                    autoFocus
                    style={renameInputStyle}
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    onBlur={() => void handleRenameCommit(canvas)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        void handleRenameCommit(canvas);
                      } else if (e.key === "Escape") {
                        setRenamingId(null);
                      }
                    }}
                  />
                ) : (
                  <span
                    style={nameStyle}
                    title={canvas.name}
                    onClick={(e) => {
                      e.stopPropagation();
                      setRenamingId(canvas.id);
                      setRenameValue(canvas.name);
                    }}
                  >
                    {canvas.name}
                  </span>
                )}
                <span style={timeStyle}>
                  {formatTime(canvas.last_opened_at)}
                  {canvas.id === canvasId
                    ? ` · ${t("canvasPanel.current")}`
                    : ""}
                </span>
              </div>
            </div>
            <button
              style={{
                ...actionBtnStyle,
                ...(confirmDeleteId === canvas.id ? deleteArmedStyle : {}),
              }}
              title={t("canvasPanel.deleteConfirm")}
              onClick={(e) => {
                e.stopPropagation();
                void handleDelete(canvas);
              }}
            >
              ✕
            </button>
          </div>
        ))}

        {nextCursor ? (
          <FilledButton
            label={loading ? "…" : t("canvasPanel.loadMore")}
            onClick={() => void refresh(true, nextCursor)}
            disabled={loading}
          />
        ) : null}

        {saveState === "saving" ? (
          <div style={statusStyle}>{t("canvasPanel.saving")}</div>
        ) : null}
        {saveState === "error" ? (
          <div style={{ ...statusStyle, color: "var(--color-danger)" }}>
            {t(
              (saveError ?? "canvasPanel.saveFailed") as Parameters<
                typeof t
              >[0],
            )}
          </div>
        ) : null}
      </div>

      <div style={footerStyle}>
        <div style={dividerStyle} />
        <FilledButton
          size="medium"
          fullWidth
          icon={PlusIcon}
          onClick={() => void handleNewCanvas()}
        >
          {t("canvasPanel.newCanvas")}
        </FilledButton>
      </div>
    </div>
  );
};

const panelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  height: "100%",
  minHeight: 0,
};

const guideStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "center",
  gap: "0.75rem",
  padding: "1.5rem 0.5rem",
  textAlign: "center",
};

const guideTextStyle: React.CSSProperties = {
  fontSize: 13,
  color: "var(--text-primary-color)",
  lineHeight: 1.5,
};

// 本地草稿卡片(匿名/登录态共用外观,复用画布条目的边框圆角)
const draftCardStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "0.375rem",
  border: "1px dashed var(--sidebar-border-color)",
  borderRadius: 8,
  padding: "0.5rem 0.625rem",
};

const draftCardHeadStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "baseline",
  justifyContent: "space-between",
  gap: "0.5rem",
  minWidth: 0,
};

const draftCardActionsStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: "0.5rem",
};

const unsavedStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--color-warning, #b8860b)",
  padding: "0.25rem 0.125rem",
};

const emptyStyle: React.CSSProperties = {
  textAlign: "center",
  fontSize: 13,
  color: "var(--text-secondary-color, #888)",
  padding: "1rem 0",
};

const listStyle: React.CSSProperties = {
  flex: "1 1 auto",
  minHeight: 0,
  overflowY: "auto",
  display: "flex",
  flexDirection: "column",
  gap: "0.375rem",
  padding: "0.75rem",
};

// 与侧边栏顶部工具栏下的分隔线同款(Sidebar.scss .sidebar__header::after):
// 1px、--sidebar-border-color、左右各 0.75rem 内缩
const dividerStyle: React.CSSProperties = {
  height: 1,
  width: "100%",
  flexShrink: 0,
  marginBottom: "0.5rem",
  background: "var(--sidebar-border-color)",
};

const footerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "0.5rem",
  padding: "0.75rem",
};

const itemStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  border: "1px solid var(--default-border-color, transparent)",
  borderRadius: 8,
  padding: "0.375rem 0.5rem",
  gap: "0.375rem",
  cursor: "pointer",
};

const itemMainStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "0.5rem",
  flex: 1,
  minWidth: 0,
};

const thumbStyle: React.CSSProperties = {
  width: 44,
  height: 33,
  objectFit: "cover",
  borderRadius: 4,
  flexShrink: 0,
  background: "var(--island-bg-color, #fff)",
};

const thumbEmptyStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  fontSize: 14,
  color: "var(--text-secondary-color, #888)",
};

const itemMetaStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  minWidth: 0,
  flex: 1,
};

const nameStyle: React.CSSProperties = {
  fontSize: 13,
  fontWeight: 500,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const timeStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-secondary-color, #888)",
};

const renameInputStyle: React.CSSProperties = {
  fontSize: 13,
  width: "100%",
  padding: "2px 4px",
  border: "1px solid var(--color-primary)",
  borderRadius: 4,
  background: "transparent",
  color: "inherit",
};

const actionBtnStyle: React.CSSProperties = {
  border: "none",
  background: "transparent",
  cursor: "pointer",
  fontSize: 12,
  padding: "4px 6px",
  borderRadius: 6,
  color: "var(--text-secondary-color, #888)",
  flexShrink: 0,
};

const deleteArmedStyle: React.CSSProperties = {
  color: "var(--color-danger, #e02e3c)",
  fontWeight: 700,
  background: "var(--color-danger-light, rgba(224, 46, 60, 0.1))",
};

const statusStyle: React.CSSProperties = {
  fontSize: 12,
  textAlign: "center",
  color: "var(--text-secondary-color, #888)",
};
