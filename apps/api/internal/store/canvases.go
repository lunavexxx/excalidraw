package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrCanvasNotFound 表示画布不存在、调用者无任何授权关系或已软删——
// 三者对调用方等价(防枚举);归属校验由 AuthorizeCanvas 前置完成。
var ErrCanvasNotFound = errors.New("store: canvas not found")

const (
	defaultCanvasName = "未命名画布"
)

// Canvas 是 canvases 行的应用层视图,Thumbnail 为 nil 表示尚未生成。
type Canvas struct {
	ID            string
	OwnerID       string
	WorkspaceID   string
	Name          string
	LatestVersion int64
	Thumbnail     *string
	LastOpenedAt  time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CanvasSummary 是列表项视图,不含重字段。
type CanvasSummary struct {
	ID            string
	Name          string
	Thumbnail     *string
	LatestVersion int64
	LastOpenedAt  time.Time
}

// CanvasScene 是场景快照;Data 为服务端原样透传的 JSONB 字节,
// FoldedUpto 为已折叠进该快照的最大事件 id(事件流折叠游标)。
type CanvasScene struct {
	Version    int64
	Data       []byte
	FoldedUpto int64
}

type CanvasFileMeta struct {
	FileID   string
	MimeType string
}

type CanvasFile struct {
	FileID   string
	MimeType string
	Data     []byte
}

const canvasColumns = `id, owner_id, workspace_id, name, latest_version, thumbnail, last_opened_at, created_at, updated_at`

// EncodeCanvasCursor / DecodeCanvasCursor 把 keyset 游标
// (last_opened_at + id)编码成不透明字符串,顺序与列表查询一致。
func EncodeCanvasCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func DecodeCanvasCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decode cursor: %w", err)
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parse cursor time: %w", err)
	}
	return t, id, nil
}

// CreateCanvas 在用户 personal 工作区创建画布(团队工作区创建是
// PR-102 的 CreateCanvasInWorkspace,仅 ws owner)。
func (db *DB) CreateCanvas(ctx context.Context, ownerID, name string) (Canvas, error) {
	if strings.TrimSpace(name) == "" {
		name = defaultCanvasName
	}
	wsID, err := db.EnsurePersonalWorkspace(ctx, ownerID)
	if err != nil {
		return Canvas{}, err
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO canvases (owner_id, workspace_id, name)
		VALUES ($1, $2, $3)
		RETURNING `+canvasColumns, ownerID, wsID, name)
	return scanCanvas(row)
}

// TouchLastOpened 刷新 last_opened_at(默认页"上次打开"依赖此字段);
// 由读详情 handler 在授权通过后调用。
func (db *DB) TouchLastOpened(ctx context.Context, canvasID string) (bool, error) {
	tag, err := db.pool.Exec(ctx,
		`UPDATE canvases SET last_opened_at = now() WHERE id = $1 AND deleted_at IS NULL`, canvasID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (db *DB) ListCanvases(ctx context.Context, ownerID string, cursor string, limit int) ([]CanvasSummary, string, error) {
	args := []any{ownerID, limit + 1}
	listQuery := `
		SELECT id, name, thumbnail, latest_version, last_opened_at
		FROM canvases
		WHERE owner_id = $1 AND deleted_at IS NULL`
	if cursor != "" {
		t, id, err := DecodeCanvasCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		listQuery += ` AND (last_opened_at, id) < ($3, $4)`
		args = append(args, t, id)
	}
	listQuery += ` ORDER BY last_opened_at DESC, id DESC LIMIT $2`
	return db.scanCanvasSummaries(ctx, limit, listQuery, args...)
}

// ListSharedCanvases 列出"与我共享":非本人拥有、但通过画布协作者或
// 工作区成员身份可及的画布(内容角色细分在打开时由 ACL 解析)。
func (db *DB) ListSharedCanvases(ctx context.Context, userID, cursor string, limit int) ([]CanvasSummary, string, error) {
	args := []any{userID, limit + 1}
	query := `
		SELECT c.id, c.name, c.thumbnail, c.latest_version, c.last_opened_at
		FROM canvases c
		WHERE c.owner_id <> $1 AND c.deleted_at IS NULL
		  AND (
		    EXISTS (SELECT 1 FROM canvas_collaborators cc
		            WHERE cc.canvas_id = c.id AND cc.user_id = $1)
		    OR EXISTS (SELECT 1 FROM workspace_members wm
		            WHERE wm.workspace_id = c.workspace_id AND wm.user_id = $1)
		  )`
	if cursor != "" {
		t, id, err := DecodeCanvasCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND (c.last_opened_at, c.id) < ($3, $4)`
		args = append(args, t, id)
	}
	query += ` ORDER BY c.last_opened_at DESC, c.id DESC LIMIT $2`
	return db.scanCanvasSummaries(ctx, limit, query, args...)
}

func (db *DB) GetCanvasScene(ctx context.Context, canvasID string) (CanvasScene, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT version, data, folded_upto FROM canvas_scenes WHERE canvas_id = $1`, canvasID)
	var s CanvasScene
	err := row.Scan(&s.Version, &s.Data, &s.FoldedUpto)
	if errors.Is(err, pgx.ErrNoRows) {
		// 画布存在但还没写过场景(懒创建后未保存)按"无场景"处理。
		return CanvasScene{}, nil
	}
	if err != nil {
		return CanvasScene{}, err
	}
	return s, nil
}

// RenameCanvas 改名;仅 canvas owner 可调(handler 以 RoleOwner 门控)。
func (db *DB) RenameCanvas(ctx context.Context, canvasID, name string) (Canvas, error) {
	if strings.TrimSpace(name) == "" {
		name = defaultCanvasName
	}
	row := db.pool.QueryRow(ctx, `
		UPDATE canvases SET name = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+canvasColumns, canvasID, name)
	canvas, err := scanCanvas(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrCanvasNotFound
	}
	return canvas, err
}

// SoftDeleteCanvas 软删;仅 canvas owner 可调(handler 以 RoleOwner 门控)。
func (db *DB) SoftDeleteCanvas(ctx context.Context, canvasID string) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE canvases SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, canvasID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCanvasNotFound
	}
	return nil
}

func (db *DB) ListCanvasFiles(ctx context.Context, canvasID string) ([]CanvasFileMeta, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT file_id, mime_type FROM canvas_files
		WHERE canvas_id = $1 ORDER BY file_id`, canvasID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metas := []CanvasFileMeta{}
	for rows.Next() {
		var m CanvasFileMeta
		if err := rows.Scan(&m.FileID, &m.MimeType); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

func (db *DB) GetCanvasFile(ctx context.Context, canvasID, fileID string) (CanvasFile, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT file_id, mime_type, data FROM canvas_files
		WHERE canvas_id = $1 AND file_id = $2`, canvasID, fileID)
	var f CanvasFile
	err := row.Scan(&f.FileID, &f.MimeType, &f.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		return CanvasFile{}, ErrCanvasNotFound
	}
	return f, err
}

// UpsertCanvasFiles 幂等写入文件字节;归属校验已由 AuthorizeCanvas 前置。
func (db *DB) UpsertCanvasFiles(ctx context.Context, canvasID string, files []CanvasFile) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, f := range files {
		if _, err := tx.Exec(ctx, `
			INSERT INTO canvas_files (canvas_id, file_id, mime_type, data)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (canvas_id, file_id) DO UPDATE
			SET mime_type = EXCLUDED.mime_type, data = EXCLUDED.data, updated_at = now()`,
			canvasID, f.FileID, f.MimeType, f.Data); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetCanvasMetaOnly 只做存在性校验,不刷新 last_opened_at
// (文件上传等伴随请求不应改变"上次打开"语义)。
func (db *DB) GetCanvasMetaOnly(ctx context.Context, canvasID string) (Canvas, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT `+canvasColumns+` FROM canvases
		WHERE id = $1 AND deleted_at IS NULL`, canvasID)
	canvas, err := scanCanvas(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrCanvasNotFound
	}
	return canvas, err
}

func scanCanvas(row rowScanner) (Canvas, error) {
	var c Canvas
	err := row.Scan(&c.ID, &c.OwnerID, &c.WorkspaceID, &c.Name, &c.LatestVersion, &c.Thumbnail, &c.LastOpenedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrCanvasNotFound
	}
	return c, err
}
