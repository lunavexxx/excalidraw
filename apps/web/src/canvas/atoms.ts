import { atom } from "../app-jotai";

/** 当前画布 id;null 表示未落库的本地草稿(打开 "/" 或新建)。 */
export const canvasIdAtom = atom<string | null>(null);

/**
 * 当前用户在画布上的内容角色(API my_role)。
 * "guest" 表示匿名分享链接访问(只读);null=个人画布 owner/未打开服务端画布。
 */
export type CanvasAccessRole = "owner" | "editor" | "viewer" | "guest";
export const canvasRoleAtom = atom<CanvasAccessRole | null>(null);

export type CanvasSaveState = "idle" | "saving" | "error";

/** 服务端自动保存状态,画布面板底部展示。 */
export const canvasSaveStateAtom = atom<CanvasSaveState>("idle");

/** 保存失败时的用户可读信息(i18n key),idle/saving 下为 null。 */
export const canvasSaveErrorAtom = atom<string | null>(null);

/** 未落库草稿是否有内容(面板「未保存草稿」指示条)。 */
export const draftDirtyAtom = atom<boolean>(false);

export const canvasCapabilitiesAtom = atom<
  import("./types").CanvasCapabilities
>({
  can_manage_collaborators: false,
  can_manage_share_links: false,
  can_review_requests: false,
});
export const onlineUsersAtom = atom<
  Map<
    string,
    { user_id: string; nickname: string; avatar_url: string; role: string }
  >
>(new Map());
export const presenceConnectedAtom = atom(false);
export const collaborationRefreshAtom = atom(0);
