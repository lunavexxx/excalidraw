-- M3 一期:工作区与画布权限底座。
-- 权限模型:共享编辑仅及画布内容;画布创建、改名/删除、协作者与分享链接
-- 管理等「信息」操作一律为拥有者专属。workspace_members 承载团队级内容
-- 角色(owner/admin/editor 映射内容 editor,viewer 映射内容 viewer),
-- canvas_collaborators 为单画布显式授予,share_links 面向匿名 guest。

CREATE TABLE workspaces (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT NOT NULL DEFAULT '我的工作区'
              CHECK (char_length(name) BETWEEN 1 AND 100),
  owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  is_personal BOOLEAN NOT NULL DEFAULT false,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 每用户至多一个 personal 工作区(注册/首次建画布时惰性创建)。
CREATE UNIQUE INDEX workspaces_owner_personal_idx
  ON workspaces(owner_id) WHERE is_personal;

CREATE TABLE workspace_members (
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role         TEXT NOT NULL CHECK (role IN ('owner','admin','editor','viewer')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE canvas_collaborators (
  canvas_id  UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL CHECK (role IN ('editor','viewer')),
  invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (canvas_id, user_id)
);

-- token 明文仅在创建响应返回一次,服务端只存 HMAC-SHA256 摘要,可撤销。
CREATE TABLE share_links (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  canvas_id  UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
  token_hash BYTEA NOT NULL UNIQUE,
  role       TEXT NOT NULL CHECK (role IN ('editor','viewer')),
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX share_links_canvas_idx ON share_links(canvas_id);

-- 存量画布归属:为每个既有用户建 personal 工作区并回填 workspace_id。
INSERT INTO workspaces (owner_id, name, is_personal)
SELECT u.id, '我的工作区', true
FROM users u
WHERE NOT EXISTS (
  SELECT 1 FROM workspaces w WHERE w.owner_id = u.id AND w.is_personal
);

INSERT INTO workspace_members (workspace_id, user_id, role)
SELECT w.id, w.owner_id, 'owner'
FROM workspaces w
WHERE w.is_personal
ON CONFLICT (workspace_id, user_id) DO NOTHING;

ALTER TABLE canvases ADD COLUMN workspace_id UUID REFERENCES workspaces(id);

UPDATE canvases c
SET workspace_id = w.id
FROM workspaces w
WHERE w.owner_id = c.owner_id AND w.is_personal;

ALTER TABLE canvases ALTER COLUMN workspace_id SET NOT NULL;

CREATE INDEX canvases_workspace_idx ON canvases(workspace_id);
