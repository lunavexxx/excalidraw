/**
 * 契约与 apps/api/internal/apiresp(canvas 段 41xxx)保持一致:
 * HTTP 恒为 200,错误全部由 code 表达(0=成功)。
 */
export type CanvasErrorCode =
  | 41000 // canvas_invalid
  | 41001 // canvas_not_found(不存在/无权限/已删除)
  | 41002 // canvas_conflict(乐观锁版本冲突)
  | 41003; // canvas_too_large

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
  } | null;
  files: { file_id: string; mime_type: string }[];
};

export type CanvasFilePayload = {
  file_id: string;
  mime_type: string;
  /** dataURL 中 base64 部分(不含 data: 前缀) */
  data: string;
};
