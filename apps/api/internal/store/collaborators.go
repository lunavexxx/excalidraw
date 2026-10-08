package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Collaborator 是 canvas_collaborators 行(附成员展示字段)。
type Collaborator struct {
	CanvasID    string
	UserID      string
	Role        string
	Nickname    string
	PhoneMasked string
	CreatedAt   time.Time
}

// ListCollaborators 列出画布协作者(管理接口,由 handler 校验分享管理能力)。
func (db *DB) ListCollaborators(ctx context.Context, canvasID string) ([]Collaborator, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT cc.canvas_id, cc.user_id, cc.role, u.nickname, u.phone_masked, cc.created_at
		FROM canvas_collaborators cc
		JOIN users u ON u.id = cc.user_id
		WHERE cc.canvas_id = $1
		ORDER BY cc.created_at ASC`, canvasID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Collaborator{}
	for rows.Next() {
		var c Collaborator
		if err := rows.Scan(&c.CanvasID, &c.UserID, &c.Role, &c.Nickname, &c.PhoneMasked, &c.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// UpsertCollaborator 幂等授予/变更单画布协作者角色。
func (db *DB) UpsertCollaborator(ctx context.Context, canvasID, userID, role, invitedBy string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO canvas_collaborators (canvas_id, user_id, role, invited_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (canvas_id, user_id) DO UPDATE
		SET role = EXCLUDED.role, invited_by = EXCLUDED.invited_by`,
		canvasID, userID, role, invitedBy)
	return err
}

// RemoveCollaborator 撤销协作者;目标不存在返回 ErrMemberNotFound。
func (db *DB) RemoveCollaborator(ctx context.Context, canvasID, userID string) error {
	tag, err := db.pool.Exec(ctx, `
		DELETE FROM canvas_collaborators WHERE canvas_id = $1 AND user_id = $2`,
		canvasID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// ShareLink 是 share_links 行(永不携带 token 原文,只存哈希)。
type ShareLink struct {
	ID        string
	CanvasID  string
	Role      string
	CreatedBy string
	ExpiresAt *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// CreateShareLink 写入分享链接(token 哈希由调用方生成)。
func (db *DB) CreateShareLink(ctx context.Context, canvasID string, tokenHash []byte, role, createdBy string, expiresAt *time.Time) (ShareLink, error) {
	row := db.pool.QueryRow(ctx, `
		INSERT INTO share_links (canvas_id, token_hash, role, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, canvas_id, role, COALESCE(created_by::text, ''), expires_at, revoked_at, created_at`,
		canvasID, tokenHash, role, createdBy, expiresAt)
	return scanShareLink(row)
}

// ListShareLinks 列出画布的分享链接(不含 token)。
func (db *DB) ListShareLinks(ctx context.Context, canvasID string) ([]ShareLink, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, canvas_id, role, COALESCE(created_by::text, ''), expires_at, revoked_at, created_at
		FROM share_links WHERE canvas_id = $1
		ORDER BY created_at DESC`, canvasID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ShareLink{}
	for rows.Next() {
		var l ShareLink
		if err := rows.Scan(&l.ID, &l.CanvasID, &l.Role, &l.CreatedBy, &l.ExpiresAt, &l.RevokedAt, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}

// RevokeShareLink 撤销链接(可重复,幂等);不存在返回 ErrCanvasNotFound。
func (db *DB) RevokeShareLink(ctx context.Context, canvasID, linkID string) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE share_links SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1 AND canvas_id = $2`, linkID, canvasID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCanvasNotFound
	}
	return nil
}

// GetActiveShareLinkByTokenHash 按哈希查有效链接(未撤销未过期);
// guest 换票与 guest JWT 复核共用。
func (db *DB) GetActiveShareLinkByTokenHash(ctx context.Context, tokenHash []byte, canvasID string) (ShareLink, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT id, canvas_id, role, COALESCE(created_by::text, ''), expires_at, revoked_at, created_at
		FROM share_links
		WHERE token_hash = $1 AND canvas_id = $2
		  AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`,
		tokenHash, canvasID)
	link, err := scanShareLink(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ShareLink{}, ErrCanvasNotFound
	}
	return link, err
}

func scanShareLink(row rowScanner) (ShareLink, error) {
	var l ShareLink
	err := row.Scan(&l.ID, &l.CanvasID, &l.Role, &l.CreatedBy, &l.ExpiresAt, &l.RevokedAt, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ShareLink{}, ErrCanvasNotFound
	}
	return l, err
}
