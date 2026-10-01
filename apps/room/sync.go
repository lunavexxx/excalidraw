// 状态补丁(协议 v2 Sync Step 2):room 收到客户端 sync-request 后,
// 回调 Go API 内部端点 scene-diff,从共享事件流(canvas_events)取
// after 之后的事件折叠差异。room 不持有状态、不复制折叠规则,
// API 是唯一事实源。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const syncDiffLimit = 2000 // 单次补丁窗口上限,与 Go API maxReadFoldEvents 对齐(has_more 分页)

type ScenePatch struct {
	Elements []json.RawMessage `json:"elements"`
	Cursor   int64             `json:"cursor"`
	HasMore  bool              `json:"has_more"`
}

type SceneSync struct {
	cfg    *Config
	client *http.Client
}

func NewSceneSync(cfg *Config) *SceneSync {
	return &SceneSync{cfg: cfg, client: &http.Client{Timeout: 5 * time.Second}}
}

type internalDiffResponse struct {
	Code int `json:"code"`
	Data *struct {
		Elements []json.RawMessage `json:"elements"`
		Cursor   int64             `json:"cursor"`
		HasMore  bool              `json:"has_more"`
	} `json:"data"`
}

// Fetch 返回 nil 表示失败(调用方回 ack error,客户端走 HTTP 兜底)。
func (s *SceneSync) Fetch(ctx context.Context, canvasID string, afterSeq int64) (*ScenePatch, error) {
	if !IsCanvasRoom(canvasID) || afterSeq < 0 {
		return nil, fmt.Errorf("invalid scene-diff request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/canvas/%s/scene-diff?after=%d&limit=%d",
			s.cfg.GoAPIURL, url.PathEscape(canvasID), afterSeq, syncDiffLimit), nil)
	if err != nil {
		return nil, fmt.Errorf("build scene-diff request: %w", err)
	}
	req.Header.Set("X-Internal-Token", s.cfg.InternalToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scene-diff request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scene-diff status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read scene-diff body: %w", err)
	}
	var parsed internalDiffResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode scene-diff: %w", err)
	}
	if parsed.Code != 0 || parsed.Data == nil || parsed.Data.Elements == nil {
		return nil, fmt.Errorf("scene-diff code=%d", parsed.Code)
	}
	cursor := parsed.Data.Cursor
	if cursor <= 0 {
		cursor = afterSeq
	}
	return &ScenePatch{
		Elements: parsed.Data.Elements,
		Cursor:   cursor,
		HasMore:  parsed.Data.HasMore,
	}, nil
}

// ParseAfterSeq 从 sync-request 载荷取 after_seq;非法一律 0。
func ParseAfterSeq(payload any) int64 {
	m, ok := payload.(map[string]any)
	if !ok {
		return 0
	}
	v, ok := m["after_seq"].(float64)
	if !ok || v < 0 || v != float64(int64(v)) {
		return 0
	}
	return int64(v)
}
