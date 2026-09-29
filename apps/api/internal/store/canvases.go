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

// ErrCanvasNotFound 表示画布不存在、不属于该用户或已软删——三者对调用方等价。
var ErrCanvasNotFound = errors.New("store: canvas not found")

// ErrCanvasVersionConflict 表示乐观锁不匹配(base_version 落后于服务端)。
var ErrCanvasVersionConflict = errors.New("store: canvas version conflict")

const (
	defaultCanvasName = "未命名画布"
)

// Canvas 是 canvases 行的应用层视图,Thumbnail 为 nil 表示尚未生成。
type Canvas struct {
	ID            string
	OwnerID       string
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

// CanvasScene 是场景快照;Data 为服务端原样透传的 JSONB 字节。
type CanvasScene struct {
	Version int64
	Data    []byte
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

const canvasColumns = `id, owner_id, name, latest_version, thumbnail, last_opened_at, created_at, updated_at`

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

func (db *DB) CreateCanvas(ctx context.Context, ownerID, name string) (Canvas, error) {
	if strings.TrimSpace(name) == "" {
		name = defaultCanvasName
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO canvases (owner_id, name)
		VALUES ($1, $2)
		RETURNING `+canvasColumns, ownerID, name)
	return scanCanvas(row)
}

func (db *DB) GetCanvas(ctx context.Context, ownerID, id string) (Canvas, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT `+canvasColumns+` FROM canvases
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, id, ownerID)
	canvas, err := scanCanvas(row)
	if err != nil {
		return Canvas{}, err
	}
	// 打开即读取:读详情同时刷新 last_opened_at(默认页"上次打开"依赖此字段)。
	_, err = db.pool.Exec(ctx,
		`UPDATE canvases SET last_opened_at = now() WHERE id = $1`, id)
	if err != nil {
		return canvas, err
	}
	canvas.LastOpenedAt = time.Now()
	return canvas, nil
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

	rows, err := db.pool.Query(ctx, listQuery, args...)
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

// SaveCanvasScene 以 base_version 为乐观锁推进场景版本:
// 先推进 canvases.latest_version(顺带落缩略图),再写 canvas_scenes 新版本行;
// 两步在事务内,场景行版本对不上则整体回滚并返回 ErrCanvasVersionConflict。
func (db *DB) SaveCanvasScene(ctx context.Context, ownerID, canvasID string, baseVersion int64, data []byte, thumbnail *string) (int64, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var newVersion int64
	err = tx.QueryRow(ctx, `
		UPDATE canvases SET latest_version = latest_version + 1, updated_at = now(),
		                   thumbnail = COALESCE($3, thumbnail)
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
		RETURNING latest_version`, canvasID, ownerID, thumbnail).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrCanvasNotFound
	}
	if err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO canvas_scenes (canvas_id, version, data)
		VALUES ($1, $2, $3)
		ON CONFLICT (canvas_id) DO UPDATE
		SET version = EXCLUDED.version, data = EXCLUDED.data, updated_at = now()
		WHERE canvas_scenes.version = $4`,
		canvasID, newVersion, data, baseVersion)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrCanvasVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return newVersion, nil
}

func (db *DB) GetCanvasScene(ctx context.Context, ownerID, canvasID string) (CanvasScene, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT cs.version, cs.data
		FROM canvas_scenes cs
		JOIN canvases c ON c.id = cs.canvas_id
		WHERE cs.canvas_id = $1 AND c.owner_id = $2 AND c.deleted_at IS NULL`,
		canvasID, ownerID)
	var s CanvasScene
	err := row.Scan(&s.Version, &s.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		// 画布存在但还没写过场景(懒创建后未保存)按"无场景"处理。
		return CanvasScene{}, nil
	}
	if err != nil {
		return CanvasScene{}, err
	}
	return s, nil
}

func (db *DB) RenameCanvas(ctx context.Context, ownerID, canvasID, name string) (Canvas, error) {
	if strings.TrimSpace(name) == "" {
		name = defaultCanvasName
	}
	row := db.pool.QueryRow(ctx, `
		UPDATE canvases SET name = $3, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
		RETURNING `+canvasColumns, canvasID, ownerID, name)
	canvas, err := scanCanvas(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrCanvasNotFound
	}
	return canvas, err
}

func (db *DB) SoftDeleteCanvas(ctx context.Context, ownerID, canvasID string) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE canvases SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, canvasID, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCanvasNotFound
	}
	return nil
}

func (db *DB) ListCanvasFiles(ctx context.Context, ownerID, canvasID string) ([]CanvasFileMeta, error) {
	if _, err := db.GetCanvasMetaOnly(ctx, ownerID, canvasID); err != nil {
		return nil, err
	}
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

func (db *DB) GetCanvasFile(ctx context.Context, ownerID, canvasID, fileID string) (CanvasFile, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT f.file_id, f.mime_type, f.data
		FROM canvas_files f
		JOIN canvases c ON c.id = f.canvas_id
		WHERE f.canvas_id = $1 AND f.file_id = $2 AND c.owner_id = $3 AND c.deleted_at IS NULL`,
		canvasID, fileID, ownerID)
	var f CanvasFile
	err := row.Scan(&f.FileID, &f.MimeType, &f.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		return CanvasFile{}, ErrCanvasNotFound
	}
	return f, err
}

// UpsertCanvasFiles 幂等写入文件字节;归属校验复用 GetCanvasMetaOnly。
func (db *DB) UpsertCanvasFiles(ctx context.Context, ownerID, canvasID string, files []CanvasFile) error {
	if _, err := db.GetCanvasMetaOnly(ctx, ownerID, canvasID); err != nil {
		return err
	}
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

// GetCanvasMetaOnly 只做存在性/归属校验,不刷新 last_opened_at
// (文件上传等伴随请求不应改变"上次打开"语义)。
func (db *DB) GetCanvasMetaOnly(ctx context.Context, ownerID, canvasID string) (Canvas, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT `+canvasColumns+` FROM canvases
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, canvasID, ownerID)
	canvas, err := scanCanvas(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrCanvasNotFound
	}
	return canvas, err
}

func scanCanvas(row rowScanner) (Canvas, error) {
	var c Canvas
	err := row.Scan(&c.ID, &c.OwnerID, &c.Name, &c.LatestVersion, &c.Thumbnail, &c.LastOpenedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrCanvasNotFound
	}
	return c, err
}
