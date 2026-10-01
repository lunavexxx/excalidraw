/**
 * 契约与 apps/api/internal/apiresp(canvas 段 41xxx)保持一致:
 * HTTP 恒为 200,错误全部由 code 表达(0=成功)。
 */
export type CanvasErrorCode =
  | 41000 // canvas_invalid
  | 41001 // canvas_not_found(不存在/无权限/已删除)
  | 41002 // canvas_conflict(乐观锁版本冲突,折叠语义后仅作兼容保留)
  | 41003 // canvas_too_large
  | 42000 // workspace/permission: 参数
  | 42001 // workspace 不存在(或非成员)
  | 42002 // 无权限(角色不足;guest 写操作)
  | 42003 // 已是成员
  | 42004 // 分享链接失效(过期/撤销/不存在)
  | 42005; // 邀请目标未注册

/** 当前请求者在画布上的内容角色(API 返回;管理类操作一律 owner 专属) */
export type CanvasRole = "owner" | "editor" | "viewer";

export type CanvasMeta = {
  id: string;
  name: string;
  latest_version: number;
  thumbnail: string | null;
  last_opened_at: string;
};

export type CanvasDetail = {
  canvas: CanvasMeta;
  scene: {
    version: number;
    data: { elements: unknown[]; appState: unknown };
    /** 读视图实际折叠到的最大事件 id(协议 v2 冷启动同步游标) */
    cursor?: number;
  } | null;
  files: { file_id: string; mime_type: string }[];
  /** 请求者对画布的内容角色(viewer/guest 只读;未返回时视为 owner 私有) */
  my_role?: CanvasRole;
};

export type CanvasFilePayload = {
  file_id: string;
  mime_type: string;
  /** dataURL 中 base64 部分(不含 data: 前缀) */
  data: string;
};

export type CollaboratorPayload = {
  user_id: string;
  nickname: string;
  phone_masked: string;
  role: "editor" | "viewer";
  created_at?: string;
};

export type ShareLinkPayload = {
  id: string;
  role: "editor" | "viewer";
  /** 明文 token 仅创建响应返回一次 */
  token?: string;
  expires_at: string | null;
  revoked_at: string | null;
  created_at: string;
};

export type GuestAccessPayload = {
  access_token: string;
  token_type: string;
  expires_in: number;
  role: "editor" | "viewer";
  canvas_id: string;
};
