-- M2 一期:画布管理(元数据 + 场景快照 + 内嵌文件)。
-- canvases 只放列表查询需要的轻字段;场景 jsonb 与文件字节独立成表,
-- 避免拖慢列表。latest_version 为保存乐观锁载体,也是后续
-- document_versions 历史表(plans/collab-platform.md)的雏形。

CREATE TABLE canvases (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT NOT NULL DEFAULT '未命名画布'
                 CHECK (char_length(name) BETWEEN 1 AND 100),
  latest_version BIGINT NOT NULL DEFAULT 0,
  thumbnail      TEXT,
  last_opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 列表按 last_opened_at 倒序 keyset 分页(游标携带 id 保证稳定序)。
CREATE INDEX canvases_owner_list_idx
  ON canvases(owner_id, last_opened_at DESC, id DESC);

CREATE TABLE canvas_scenes (
  canvas_id  UUID PRIMARY KEY REFERENCES canvases(id) ON DELETE CASCADE,
  version    BIGINT NOT NULL,
  data       JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE canvas_files (
  canvas_id  UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
  file_id    TEXT NOT NULL,
  mime_type  TEXT NOT NULL,
  data       BYTEA NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (canvas_id, file_id)
);
