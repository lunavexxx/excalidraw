import { atom } from "../app-jotai";

/** 当前画布 id;null 表示未落库的本地草稿(打开 "/" 或新建)。 */
export const canvasIdAtom = atom<string | null>(null);

export type CanvasSaveState = "idle" | "saving" | "error";

/** 服务端自动保存状态,画布面板底部展示。 */
export const canvasSaveStateAtom = atom<CanvasSaveState>("idle");

/** 保存失败时的用户可读信息(i18n key),idle/saving 下为 null。 */
export const canvasSaveErrorAtom = atom<string | null>(null);

/** 未落库草稿是否有内容(面板「未保存草稿」指示条)。 */
export const draftDirtyAtom = atom<boolean>(false);
