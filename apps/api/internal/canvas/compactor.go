// compactor.go:后台折叠程序——把 canvas_events 中未折叠事件按纯规则
// 合并进 canvas_scenes 快照(唯一事实),推进 folded_upto 并按保留期清理。
// 等价于"后台程序抢占画布 → 本地重放 → 导出完整备份"的持久化循环。
package canvas

import (
	"context"
	"log"
	"time"

	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

const (
	compactorInterval = 5 * time.Second
	compactorBatch    = 200
	// 事件保留期:折叠后保留 7 天供回放/审计(P3 编辑历史的基底),
	// 由折叠事务内顺带清理。
	eventRetentionDays = 7
)

// StartCompactor 启动后台折叠循环;ctx 取消即停止。
func StartCompactor(ctx context.Context, db *store.DB) {
	go func() {
		ticker := time.NewTicker(compactorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				compactOnce(ctx, db)
			}
		}
	}()
}

func compactOnce(ctx context.Context, db *store.DB) {
	canvasIDs, err := db.ListCanvasesWithPendingEvents(ctx, compactorBatch)
	if err != nil {
		log.Printf("compactor: list pending canvases: %v", err)
		return
	}
	for _, canvasID := range canvasIDs {
		_, folded, err := db.FoldPendingEvents(ctx, canvasID, eventRetentionDays)
		if err != nil {
			log.Printf("compactor: fold canvas %s: %v", canvasID, err)
			continue
		}
		_ = folded
	}
}
