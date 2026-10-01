// 进房 ACL:回调 Go API 内部端点解析内容角色。
// fail-closed:回调失败或角色为空一律拒绝进房(Go API 不可用时场景 GET
// 同样不可用,fail-open 无收益)。结果按连接缓存,TTL 不超过 token 有效期。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const aclCacheTTL = int64(60) // 秒

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func IsCanvasRoom(roomID string) bool {
	return uuidRe.MatchString(roomID)
}

type ACLResolver struct {
	cfg    *Config
	client *http.Client
}

func NewACLResolver(cfg *Config) *ACLResolver {
	return &ACLResolver{cfg: cfg, client: &http.Client{Timeout: 5 * time.Second}}
}

type internalAccessResponse struct {
	Code int `json:"code"`
	Data *struct {
		Role string `json:"role"`
	} `json:"data"`
}

// ResolveRole 返回 "" 表示拒绝进房(无关系/链接失效/回调失败)。
func (a *ACLResolver) ResolveRole(ctx context.Context, session *Session, canvasID string) (string, error) {
	now := time.Now().Unix()
	if role, ok := session.cachedACL(canvasID, now); ok {
		return role, nil
	}

	role := a.fetchRole(ctx, session, canvasID)
	// 缓存拒绝结果与放行同等重要:API 抖动期不放大回调量。
	ttl := aclCacheTTL
	if session.TokenExp > 0 && session.TokenExp-now < ttl {
		ttl = session.TokenExp - now
	}
	if ttl < 0 {
		ttl = aclCacheTTL
	}
	session.storeACL(canvasID, role, now+ttl)
	return role, nil
}

func (a *ACLResolver) fetchRole(ctx context.Context, session *Session, canvasID string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/canvas/%s/access", a.cfg.GoAPIURL, url.PathEscape(canvasID)), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("X-Internal-Token", a.cfg.InternalToken)

	resp, err := a.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ""
	}
	var parsed internalAccessResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if parsed.Code != 0 || parsed.Data == nil {
		return ""
	}
	switch parsed.Data.Role {
	case "owner", "editor", "viewer":
		return parsed.Data.Role
	default:
		return ""
	}
}
