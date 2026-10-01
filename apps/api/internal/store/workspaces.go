package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrWorkspaceNotFound 工作区不存在或调用者不是成员(防枚举合一)。
	ErrWorkspaceNotFound = errors.New("store: workspace not found")
	// ErrAlreadyMember 目标用户已是工作区成员。
	ErrAlreadyMember = errors.New("store: already a member")
	// ErrMemberNotFound 成员不存在。
	ErrMemberNotFound = errors.New("store: member not found")
	// ErrCannotModifyOwner 工作区 owner 不可被降级/移除/改角色。
	ErrCannotModifyOwner = errors.New("store: cannot modify workspace owner")
)

// WorkspaceRoleRank 用于 handler 层的角色门槛比较;owner > admin > editor > viewer。
var WorkspaceRoleRank = map[string]int{
	"owner": 4, "admin": 3, "editor": 2, "viewer": 1,
}

// WorkspaceRoleAtLeast 报告成员角色是否达到门槛(未知角色视为不满足)。
func WorkspaceRoleAtLeast(role, min string) bool {
	return WorkspaceRoleRank[role] >= WorkspaceRoleRank[min]
}

type Workspace struct {
	ID         string
	OwnerID    string
	Name       string
	IsPersonal bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WorkspaceWithRole 在列表场景附带当前用户在其中的角色。
type WorkspaceWithRole struct {
	Workspace
	MyRole string
}

// WorkspaceMember 附带成员展示字段(昵称/脱敏手机号)。
type WorkspaceMember struct {
	WorkspaceID string
	UserID      string
	Role        string
	Nickname    string
	PhoneMasked string
	CreatedAt   time.Time
}

const workspaceColumns = `id, owner_id, name, is_personal, created_at, updated_at`

// CreateTeamWorkspace 创建团队工作区并写入 owner 成员。
func (db *DB) CreateTeamWorkspace(ctx context.Context, ownerID, name string) (Workspace, error) {
	if strings.TrimSpace(name) == "" {
		name = "未命名工作区"
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var w Workspace
	err = tx.QueryRow(ctx, `
		INSERT INTO workspaces (owner_id, name, is_personal)
		VALUES ($1, $2, false)
		RETURNING `+workspaceColumns, ownerID, name).
		Scan(&w.ID, &w.OwnerID, &w.Name, &w.IsPersonal, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Workspace{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner') ON CONFLICT DO NOTHING`, w.ID, ownerID); err != nil {
		return Workspace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

// ListUserWorkspaces 返回用户的 personal + 已加入团队工作区(附我的角色)。
func (db *DB) ListUserWorkspaces(ctx context.Context, userID string) ([]WorkspaceWithRole, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT w.id, w.owner_id, w.name, w.is_personal, w.created_at, w.updated_at,
		       COALESCE(wm.role, CASE WHEN w.owner_id = $1 THEN 'owner' END) AS my_role
		FROM workspaces w
		LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = $1
		WHERE w.owner_id = $1 OR wm.user_id = $1
		ORDER BY w.is_personal DESC, w.created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkspaceWithRole{}
	for rows.Next() {
		var w WorkspaceWithRole
		if err := rows.Scan(&w.ID, &w.OwnerID, &w.Name, &w.IsPersonal, &w.CreatedAt, &w.UpdatedAt, &w.MyRole); err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	return items, rows.Err()
}

// GetWorkspace 返回工作区与成员列表;调用者须已是成员(handler 前置校验)。
func (db *DB) GetWorkspace(ctx context.Context, wsID string) (Workspace, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT `+workspaceColumns+` FROM workspaces WHERE id = $1`, wsID)
	var w Workspace
	err := row.Scan(&w.ID, &w.OwnerID, &w.Name, &w.IsPersonal, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return w, err
}

func (db *DB) ListWorkspaceMembers(ctx context.Context, wsID string) ([]WorkspaceMember, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT wm.workspace_id, wm.user_id, wm.role, u.nickname, u.phone_masked, wm.created_at
		FROM workspace_members wm
		JOIN users u ON u.id = wm.user_id
		WHERE wm.workspace_id = $1
		ORDER BY CASE wm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, wm.created_at ASC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []WorkspaceMember{}
	for rows.Next() {
		var m WorkspaceMember
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.Nickname, &m.PhoneMasked, &m.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// WorkspaceRoleOf 返回用户在工作区中的角色,非成员返回空串。
func (db *DB) WorkspaceRoleOf(ctx context.Context, wsID, userID string) (string, error) {
	var role string
	err := db.pool.QueryRow(ctx, `
		SELECT COALESCE(wm.role, CASE WHEN w.owner_id = $2 THEN 'owner' END)
		FROM workspaces w
		LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = $2
		WHERE w.id = $1`, wsID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// RenameWorkspace 改名;门槛由 handler 校验(仅 owner/admin)。
func (db *DB) RenameWorkspace(ctx context.Context, wsID, name string) (Workspace, error) {
	if strings.TrimSpace(name) == "" {
		return Workspace{}, ErrWorkspaceNotFound
	}
	row := db.pool.QueryRow(ctx, `
		UPDATE workspaces SET name = $2, updated_at = now()
		WHERE id = $1 RETURNING `+workspaceColumns, wsID, name)
	var w Workspace
	err := row.Scan(&w.ID, &w.OwnerID, &w.Name, &w.IsPersonal, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return w, err
}

// AddMember 幂等失败式:已存在成员返回 ErrAlreadyMember。
func (db *DB) AddMember(ctx context.Context, wsID, userID, role string) error {
	tag, err := db.pool.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO NOTHING`, wsID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyMember
	}
	return nil
}

// UpdateMemberRole 变更成员角色;owner 角色成员不可被修改。
func (db *DB) UpdateMemberRole(ctx context.Context, wsID, userID, role string) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE workspace_members SET role = $3
		WHERE workspace_id = $1 AND user_id = $2 AND role <> 'owner'`, wsID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// 区分:成员不存在 vs 是 owner。
	var cur string
	err = db.pool.QueryRow(ctx, `
		SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		wsID, userID).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return err
	}
	if cur == "owner" {
		return ErrCannotModifyOwner
	}
	return ErrMemberNotFound
}

// RemoveMember 移除成员;owner 不可被移除。
func (db *DB) RemoveMember(ctx context.Context, wsID, userID string) error {
	tag, err := db.pool.Exec(ctx, `
		DELETE FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2 AND role <> 'owner'`, wsID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var cur string
	err = db.pool.QueryRow(ctx, `
		SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		wsID, userID).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return err
	}
	if cur == "owner" {
		return ErrCannotModifyOwner
	}
	return ErrMemberNotFound
}

// ListWorkspaceCanvases 列出工作区内画布(内容角色校验由调用方前置;
// 成员身份在本方法内复核,防越权直读)。
func (db *DB) ListWorkspaceCanvases(ctx context.Context, wsID, userID, cursor string, limit int) ([]CanvasSummary, string, error) {
	role, err := db.WorkspaceRoleOf(ctx, wsID, userID)
	if err != nil {
		return nil, "", err
	}
	if role == "" {
		return nil, "", ErrWorkspaceNotFound
	}
	args := []any{wsID, limit + 1}
	query := `
		SELECT id, name, thumbnail, latest_version, last_opened_at
		FROM canvases
		WHERE workspace_id = $1 AND deleted_at IS NULL`
	if cursor != "" {
		t, id, err := DecodeCanvasCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND (last_opened_at, id) < ($3, $4)`
		args = append(args, t, id)
	}
	query += ` ORDER BY last_opened_at DESC, id DESC LIMIT $2`
	return db.scanCanvasSummaries(ctx, limit, query, args...)
}

// scanCanvasSummaries 执行列表查询(limit 参数需为 args 中的"limit+1")
// 并统一处理 keyset 游标截断。
func (db *DB) scanCanvasSummaries(ctx context.Context, limit int, query string, args ...any) ([]CanvasSummary, string, error) {
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []CanvasSummary{}
	for rows.Next() {
		var s CanvasSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.Thumbnail, &s.LatestVersion, &s.LastOpenedAt); err != nil {
			return nil, "", err
		}
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = EncodeCanvasCursor(last.LastOpenedAt, last.ID)
		items = items[:limit]
	}
	return items, next, nil
}
