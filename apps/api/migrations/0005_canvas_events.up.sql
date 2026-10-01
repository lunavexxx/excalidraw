-- M3 二期:事件流持久化(到达即落库 + 后台折叠)。
-- room 服务收到每条场景广播后立即 INSERT 一行;Go compactor 周期把
-- id > canvas_scenes.folded_upto 的事件按元素版本纯规则折叠进快照。
-- 全局单调递增的 id 即每画布的 seq,不单设 seq 列。

CREATE TABLE canvas_events (
  id         BIGSERIAL PRIMARY KEY,
  canvas_id  UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
  elements   JSONB NOT NULL,
  app_state  JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX canvas_events_canvas_id_idx ON canvas_events(canvas_id, id);

-- 折叠游标:该画布已被折叠进快照的最大事件 id。
ALTER TABLE canvas_scenes ADD COLUMN folded_upto BIGINT NOT NULL DEFAULT 0;
