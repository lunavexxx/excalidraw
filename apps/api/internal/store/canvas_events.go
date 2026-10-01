package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/lunavexxx/excalidraw/apps/api/internal/scene"
)

// CanvasEvent 是一条已到达即落库的场景广播(元素状态快照批)。
// id 即每画布的 seq(全局单调),折叠游标与之比较。
type CanvasEvent struct {
	ID       int64
	Elements []json.RawMessage
	AppState json.RawMessage
}

// ListPendingEvents 返回 id > after 的事件(升序);limit 上限由调用方
// 控制(读路径折叠窗口有界,压缩滞后时宁可返回部分)。
func (db *DB) ListPendingEvents(ctx context.Context, canvasID string, after int64, limit int) ([]CanvasEvent, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, elements, app_state FROM canvas_events
		WHERE canvas_id = $1 AND id > $2
		ORDER BY id ASC LIMIT $3`, canvasID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []CanvasEvent{}
	for rows.Next() {
		var e CanvasEvent
		var elements, appState json.RawMessage
		if err := rows.Scan(&e.ID, &elements, &appState); err != nil {
			return nil, err
		}
		e.AppState = appState
		if err := json.Unmarshal(elements, &e.Elements); err != nil {
			// 单条事件损坏不阻塞整体(纯折叠跳过坏项由客户端兜底);
			// 这里整条保留为空批,行为等价于跳过。
			e.Elements = nil
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ListCanvasesWithPendingEvents 找出有待折叠事件的画布(软删除外),
// 按最新事件 id 倒序(优先压缩活跃画布)。
func (db *DB) ListCanvasesWithPendingEvents(ctx context.Context, limit int) ([]string, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT ce.canvas_id
		FROM canvas_events ce
		JOIN canvases c ON c.id = ce.canvas_id AND c.deleted_at IS NULL
		LEFT JOIN canvas_scenes cs ON cs.canvas_id = ce.canvas_id
		WHERE cs.canvas_id IS NULL OR ce.id > cs.folded_upto
		GROUP BY ce.canvas_id
		ORDER BY MAX(ce.id) DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FoldPendingEvents 把画布的待折叠事件按纯规则折叠进 canvas_scenes 快照:
// advisory lock 抢占单画布 → 读基础快照与事件 → fold → 写回快照并推进
// folded_upto → 清理超过保留期的已折叠事件。幂等:无 pending 时为 no-op。
// 返回 (foldedUpto, foldedCount)。
func (db *DB) FoldPendingEvents(ctx context.Context, canvasID string, retentionDays int) (int64, int, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 以画布为粒度串行化折叠(多 compactor/多实例安全)。
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, canvasID); err != nil {
		return 0, 0, err
	}

	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT true FROM canvases WHERE id = $1 AND deleted_at IS NULL`, canvasID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrCanvasNotFound
	}
	if err != nil {
		return 0, 0, err
	}

	var baseData []byte
	var foldedUpto int64
	err = tx.QueryRow(ctx,
		`SELECT data, folded_upto FROM canvas_scenes WHERE canvas_id = $1`, canvasID).
		Scan(&baseData, &foldedUpto)
	if errors.Is(err, pgx.ErrNoRows) {
		baseData, foldedUpto = nil, 0
	} else if err != nil {
		return 0, 0, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, elements, app_state FROM canvas_events
		WHERE canvas_id = $1 AND id > $2
		ORDER BY id ASC`, canvasID, foldedUpto)
	if err != nil {
		return 0, 0, err
	}
	type pending struct {
		id       int64
		elements json.RawMessage
		appState json.RawMessage
	}
	events := []pending{}
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.elements, &p.appState); err != nil {
			rows.Close()
			return 0, 0, err
		}
		events = append(events, p)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	rows.Close()

	if len(events) == 0 {
		return foldedUpto, 0, nil
	}

	baseElements, appState, err := scene.SplitSceneData(baseData)
	if err != nil {
		return 0, 0, err
	}
	deltas := make([][]json.RawMessage, 0, len(events))
	maxSeq := foldedUpto
	for _, p := range events {
		var els []json.RawMessage
		if err := json.Unmarshal(p.elements, &els); err != nil {
			continue // 坏事件跳过,不阻塞折叠
		}
		deltas = append(deltas, els)
		if p.appState != nil {
			appState = p.appState
		}
		if p.id > maxSeq {
			maxSeq = p.id
		}
	}
	folded := scene.FoldElements(baseElements, deltas...)
	data, err := scene.BuildSceneData(folded, appState)
	if err != nil {
		return 0, 0, err
	}

	// 内容变化即推进 advisory 版本号(与 PUT 折叠同一语义)。
	var newVersion int64
	err = tx.QueryRow(ctx, `
		UPDATE canvases SET latest_version = latest_version + 1, updated_at = now()
		WHERE id = $1 RETURNING latest_version`, canvasID).Scan(&newVersion)
	if err != nil {
		return 0, 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO canvas_scenes (canvas_id, version, data, folded_upto, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (canvas_id) DO UPDATE
		SET version = EXCLUDED.version, data = EXCLUDED.data,
		    folded_upto = EXCLUDED.folded_upto, updated_at = now()`,
		canvasID, newVersion, data, maxSeq); err != nil {
		return 0, 0, err
	}

	if retentionDays > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM canvas_events
			WHERE canvas_id = $1 AND id <= $2
			  AND created_at < now() - make_interval(days => $3)`,
			canvasID, maxSeq, retentionDays); err != nil {
			return 0, 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return maxSeq, len(events), nil
}

// FoldCanvasScene 是 PUT /canvases/:id/scene 的折叠写入语义:
// 把客户端整场景元素按纯规则合并进快照(非替换),appState 随 PUT 覆盖
// (LWW)。base_version 不再作为闸门(元素级版本即事实),latest_version
// 仍推进作 advisory。画布不存在返回 ErrCanvasNotFound。
func (db *DB) FoldCanvasScene(ctx context.Context, canvasID string, elements []json.RawMessage, appState json.RawMessage, thumbnail *string) (int64, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, canvasID); err != nil {
		return 0, err
	}

	var newVersion int64
	err = tx.QueryRow(ctx, `
		UPDATE canvases SET latest_version = latest_version + 1, updated_at = now(),
		                   thumbnail = COALESCE($2, thumbnail)
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING latest_version`, canvasID, thumbnail).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrCanvasNotFound
	}
	if err != nil {
		return 0, err
	}

	var baseData []byte
	err = tx.QueryRow(ctx,
		`SELECT data FROM canvas_scenes WHERE canvas_id = $1`, canvasID).Scan(&baseData)
	if errors.Is(err, pgx.ErrNoRows) {
		baseData = nil
	} else if err != nil {
		return 0, err
	}

	baseElements, existingAppState, err := scene.SplitSceneData(baseData)
	if err != nil {
		return 0, err
	}
	if appState != nil {
		existingAppState = appState
	}
	folded := scene.FoldElements(baseElements, elements)
	data, err := scene.BuildSceneData(folded, existingAppState)
	if err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO canvas_scenes (canvas_id, version, data, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (canvas_id) DO UPDATE
		SET version = EXCLUDED.version, data = EXCLUDED.data, updated_at = now()`,
		canvasID, newVersion, data); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return newVersion, nil
}
