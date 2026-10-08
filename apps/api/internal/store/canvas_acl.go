package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// CanvasRole 是画布内容角色；分享管理能力独立建模于 CanvasCapabilities。
type CanvasRole string

const (
	RoleOwner  CanvasRole = "owner"
	RoleEditor CanvasRole = "editor"
	RoleViewer CanvasRole = "viewer"
	RoleNone   CanvasRole = ""
)

func (r CanvasRole) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// AtLeast 报告角色是否达到最低要求。
func (r CanvasRole) AtLeast(min CanvasRole) bool { return r.rank() >= min.rank() }

// ResolveCanvasRole 是 ACL 合并规则的唯一事实(角色矩阵):
// canvas owner 最高;workspace owner/admin/editor → 内容 editor;
// canvas_collaborators editor 次之;viewer 类再次;否则无权限。
// 纯函数,SQL 只负责取出 (owner_id, ws_role, cc_role)。
func ResolveCanvasRole(ownerID, userID, wsRole, ccRole string) CanvasRole {
	if ownerID != "" && ownerID == userID {
		return RoleOwner
	}
	if wsRole == "owner" || wsRole == "admin" || wsRole == "editor" {
		return RoleEditor
	}
	if ccRole == "editor" {
		return RoleEditor
	}
	if wsRole == "viewer" || ccRole == "viewer" {
		return RoleViewer
	}
	return RoleNone
}

// AccessIdentity 是授权输入:登录用户或匿名 guest(分享链接)。
type AccessIdentity struct {
	UserID string
	Guest  *GuestIdentity
}

// GuestIdentity 来自已验签的 guest JWT;ShareLinkID 供逐请求复核链接有效性。
type GuestIdentity struct {
	Subject     string
	ShareLinkID string
	CanvasID    string
	Role        string
}

// AuthorizeCanvas 解析 (role, canvas)。画布不存在或与身份无任何授权关系
// 一律 ErrCanvasNotFound(防枚举);"存在但角色不足"由调用方按 role 判定。
func (db *DB) AuthorizeCanvas(ctx context.Context, ident AccessIdentity, canvasID string) (CanvasRole, Canvas, error) {
	if ident.Guest != nil {
		return db.authorizeGuest(ctx, *ident.Guest, canvasID)
	}
	return db.authorizeUser(ctx, ident.UserID, canvasID)
}

func (db *DB) authorizeUser(ctx context.Context, userID, canvasID string) (CanvasRole, Canvas, error) {
	row := db.pool.QueryRow(ctx, `
		WITH cv AS (
			SELECT `+canvasColumns+` FROM canvases WHERE id = $1 AND deleted_at IS NULL
		),
		ws AS (
			SELECT role FROM workspace_members
			WHERE workspace_id = (SELECT workspace_id FROM cv) AND user_id = $2
		),
		cc AS (
			SELECT role FROM canvas_collaborators WHERE canvas_id = $1 AND user_id = $2
		)
		SELECT cv.id, cv.owner_id, cv.workspace_id, cv.name, cv.latest_version,
		       cv.thumbnail, cv.last_opened_at, cv.created_at, cv.updated_at,
		       CASE
		           WHEN cv.owner_id = $2 THEN 'owner'
		           WHEN ws.role IN ('owner','admin','editor') OR cc.role = 'editor' THEN 'editor'
		           WHEN ws.role = 'viewer' OR cc.role = 'viewer' THEN 'viewer'
		           ELSE NULL
		       END
		FROM cv LEFT JOIN ws ON TRUE LEFT JOIN cc ON TRUE`, canvasID, userID)

	var role *string
	var c Canvas
	err := row.Scan(&c.ID, &c.OwnerID, &c.WorkspaceID, &c.Name, &c.LatestVersion,
		&c.Thumbnail, &c.LastOpenedAt, &c.CreatedAt, &c.UpdatedAt, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleNone, Canvas{}, ErrCanvasNotFound
	}
	if err != nil {
		return RoleNone, Canvas{}, err
	}
	if role == nil {
		return RoleNone, Canvas{}, ErrCanvasNotFound
	}
	return CanvasRole(*role), c, nil
}

// authorizeGuest 校验 guest JWT 绑定的画布并逐请求复核分享链接有效性
// (撤销/过期即时生效;角色信任 JWT 签名,写权限由 handler 层钳制)。
func (db *DB) authorizeGuest(ctx context.Context, g GuestIdentity, canvasID string) (CanvasRole, Canvas, error) {
	if g.CanvasID != canvasID {
		return RoleNone, Canvas{}, ErrCanvasNotFound
	}
	canvas, err := db.GetCanvasMetaOnly(ctx, canvasID)
	if err != nil {
		return RoleNone, Canvas{}, err
	}
	var valid bool
	err = db.pool.QueryRow(ctx, `
		SELECT revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		FROM share_links WHERE id = $1 AND canvas_id = $2`,
		g.ShareLinkID, canvasID).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleNone, Canvas{}, ErrCanvasNotFound
	}
	if err != nil {
		return RoleNone, Canvas{}, err
	}
	if !valid {
		return RoleNone, Canvas{}, ErrCanvasNotFound
	}
	role := RoleViewer
	if g.Role == "editor" {
		role = RoleEditor
	}
	return role, canvas, nil
}

// EnsurePersonalWorkspace 返回用户的 personal 工作区 ID,缺失则惰性创建
// (注册流程也会调用;并发创建由 partial unique index 兜底)。
func (db *DB) EnsurePersonalWorkspace(ctx context.Context, userID string) (string, error) {
	wsID, err := db.personalWorkspaceID(ctx, userID)
	if err == nil {
		return wsID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if _, err := db.pool.Exec(ctx, `
		INSERT INTO workspaces (owner_id, name, is_personal)
		VALUES ($1, '我的工作区', true)
		ON CONFLICT DO NOTHING`, userID); err != nil {
		return "", err
	}
	if _, err := db.pool.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		SELECT w.id, w.owner_id, 'owner' FROM workspaces w
		WHERE w.owner_id = $1 AND w.is_personal
		ON CONFLICT (workspace_id, user_id) DO NOTHING`, userID); err != nil {
		return "", err
	}
	return db.personalWorkspaceID(ctx, userID)
}

func (db *DB) personalWorkspaceID(ctx context.Context, userID string) (string, error) {
	var wsID string
	err := db.pool.QueryRow(ctx, `
		SELECT id FROM workspaces WHERE owner_id = $1 AND is_personal LIMIT 1`,
		userID).Scan(&wsID)
	return wsID, err
}
