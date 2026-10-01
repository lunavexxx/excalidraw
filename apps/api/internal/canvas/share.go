// share.go:画布协作者与分享链接管理(信息类操作,一律 canvas owner 专属)
// 以及匿名 guest 换票端点。协作者/分享链接的变更不影响场景内容本身。
package canvas

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

// shareLinkTokenTTL 是 guest JWT 的有效期(≤12h,取 1h 平衡撤销延迟与体验;
// 过期后客户端持 URL 中的 share token 重新换票)。
const shareLinkTokenTTL = time.Hour

// registerShareRoutes 在 /canvases 组内追加协作者与分享链接路由。
// 这些都是信息类操作,统一 RoleOwner 门控。
func registerShareRoutes(g *gin.RouterGroup, h *canvasHandlers) {
	g.GET("/:id/collaborators", h.authorize(store.RoleOwner), h.listCollaborators)
	g.PUT("/:id/collaborators", h.authorize(store.RoleOwner), h.putCollaborator)
	g.DELETE("/:id/collaborators/:userId", h.authorize(store.RoleOwner), h.removeCollaborator)
	g.GET("/:id/share-links", h.authorize(store.RoleOwner), h.listShareLinks)
	g.POST("/:id/share-links", h.authorize(store.RoleOwner), h.createShareLink)
	g.DELETE("/:id/share-links/:linkId", h.authorize(store.RoleOwner), h.revokeShareLink)
}

// newShareLinkToken 生成 32 字节随机 token(base64url 给客户端)与其
// sha256 摘要(入库;与 refresh token 同一模式,库中不存原文)。
func newShareLinkToken() (raw string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256(buf)
	return raw, sum[:], nil
}

func hashShareLinkToken(raw string) ([]byte, error) {
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(buf) != 32 {
		return nil, errors.New("malformed share token")
	}
	sum := sha256.Sum256(buf)
	return sum[:], nil
}

type collaboratorPayload struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	PhoneMasked string `json:"phone_masked"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at"`
}

func (h *canvasHandlers) listCollaborators(c *gin.Context) {
	items, err := h.db.ListCollaborators(c.Request.Context(), c.Param("id"))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "list collaborators failed")
		return
	}
	resp := make([]collaboratorPayload, 0, len(items))
	for _, it := range items {
		resp = append(resp, collaboratorPayload{
			UserID:      it.UserID,
			Nickname:    it.Nickname,
			PhoneMasked: it.PhoneMasked,
			Role:        it.Role,
			CreatedAt:   it.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	apiresp.OK(c, gin.H{"items": resp})
}

type putCollaboratorRequest struct {
	Phone string `json:"phone" binding:"required"`
	Role  string `json:"role" binding:"required"`
}

// phoneCryptoReady 报告手机号加密组件是否可用(未配置时手机号类操作不可用)。
func (h *canvasHandlers) phoneCryptoReady() bool {
	return h.phoneCrypto != nil
}

func (h *canvasHandlers) putCollaborator(c *gin.Context) {
	var req putCollaboratorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "phone and role are required")
		return
	}
	if req.Role != "editor" && req.Role != "viewer" {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "role must be editor or viewer")
		return
	}
	phone, err := auth.NormalizePhone(req.Phone)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid phone")
		return
	}
	if !h.phoneCryptoReady() {
		apiresp.Fail(c, apiresp.CodeInternal, "phone crypto not configured")
		return
	}
	target, err := h.db.GetUserByPhoneHash(c.Request.Context(), h.phoneCrypto.Hash(phone))
	if errors.Is(err, store.ErrUserNotFound) {
		apiresp.Fail(c, apiresp.CodeInviteTargetMissing, "user not registered")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "lookup user failed")
		return
	}
	if err := h.db.UpsertCollaborator(c.Request.Context(), c.Param("id"), target.ID, req.Role, auth.UserIDFrom(c)); err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "save collaborator failed")
		return
	}
	apiresp.OK(c, gin.H{"collaborator": collaboratorPayload{
		UserID:      target.ID,
		Nickname:    target.Nickname,
		PhoneMasked: target.PhoneMasked,
		Role:        req.Role,
	}})
}

func (h *canvasHandlers) removeCollaborator(c *gin.Context) {
	err := h.db.RemoveCollaborator(c.Request.Context(), c.Param("id"), c.Param("userId"))
	if errors.Is(err, store.ErrMemberNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "collaborator not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "remove collaborator failed")
		return
	}
	apiresp.OK(c, nil)
}

type shareLinkPayload struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	Token     string  `json:"token,omitempty"` // 明文仅创建响应返回一次
	ExpiresAt *string `json:"expires_at"`
	RevokedAt *string `json:"revoked_at"`
	CreatedAt string  `json:"created_at"`
}

func shareLinkPayloadFrom(l store.ShareLink) shareLinkPayload {
	p := shareLinkPayload{
		ID:        l.ID,
		Role:      l.Role,
		CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
	}
	if l.ExpiresAt != nil {
		t := l.ExpiresAt.UTC().Format(time.RFC3339)
		p.ExpiresAt = &t
	}
	if l.RevokedAt != nil {
		t := l.RevokedAt.UTC().Format(time.RFC3339)
		p.RevokedAt = &t
	}
	return p
}

func (h *canvasHandlers) listShareLinks(c *gin.Context) {
	items, err := h.db.ListShareLinks(c.Request.Context(), c.Param("id"))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "list share links failed")
		return
	}
	resp := make([]shareLinkPayload, 0, len(items))
	for _, it := range items {
		resp = append(resp, shareLinkPayloadFrom(it))
	}
	apiresp.OK(c, gin.H{"items": resp})
}

type createShareLinkRequest struct {
	Role          string `json:"role" binding:"required"`
	ExpiresInDays int    `json:"expires_in_days"`
}

func (h *canvasHandlers) createShareLink(c *gin.Context) {
	var req createShareLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "role is required")
		return
	}
	if req.Role != "editor" && req.Role != "viewer" {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "role must be editor or viewer")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 365 {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "expires_in_days must be 0-365")
		return
	}
	raw, hash, err := newShareLinkToken()
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "generate token failed")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresInDays)
		expiresAt = &t
	}
	link, err := h.db.CreateShareLink(c.Request.Context(), c.Param("id"), hash, req.Role, auth.UserIDFrom(c), expiresAt)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "create share link failed")
		return
	}
	payload := shareLinkPayloadFrom(link)
	payload.Token = raw
	apiresp.OK(c, gin.H{"share_link": payload})
}

func (h *canvasHandlers) revokeShareLink(c *gin.Context) {
	err := h.db.RevokeShareLink(c.Request.Context(), c.Param("id"), c.Param("linkId"))
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "share link not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "revoke share link failed")
		return
	}
	apiresp.OK(c, nil)
}

// ---- guest 换票(匿名;持 URL 中的 share token 换取短期 guest JWT)----

// ipRateLimiter 是极简滑动窗口限流(guest 换票是匿名端点)。
type ipRateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

func (r *ipRateLimiter) allow(key string) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	hits := r.hits[key][:0]
	for _, t := range r.hits[key] {
		if now.Sub(t) < r.window {
			hits = append(hits, t)
		}
	}
	if len(hits) >= r.limit {
		r.hits[key] = hits
		return false
	}
	r.hits[key] = append(hits, now)
	// 惰性清理:map 过大时整体重扫(规模小,可接受)。
	if len(r.hits) > 4096 {
		for k, v := range r.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= r.window {
				delete(r.hits, k)
			}
		}
	}
	return true
}

type guestAccessRequest struct {
	ShareToken string `json:"share_token" binding:"required"`
}

// guestAccess 是唯一的匿名端点:POST /canvases/:id/access。
// 校验 share token 后签发绑定该画布的 guest JWT(含角色)。
func (h *canvasHandlers) guestAccess(c *gin.Context) {
	if !h.guestLimiter.allow(c.ClientIP()) {
		apiresp.Fail(c, apiresp.CodeShareLinkInvalid, "too many requests")
		return
	}
	var req guestAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "share_token is required")
		return
	}
	hash, err := hashShareLinkToken(strings.TrimSpace(req.ShareToken))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeShareLinkInvalid, "invalid share token")
		return
	}
	link, err := h.db.GetActiveShareLinkByTokenHash(c.Request.Context(), hash, c.Param("id"))
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeShareLinkInvalid, "share link invalid or expired")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "resolve share link failed")
		return
	}
	token, expiresIn, err := auth.SignGuestToken(h.jwtSecret, link.ID, link.CanvasID, link.Role, shareLinkTokenTTL)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "issue guest token failed")
		return
	}
	apiresp.OK(c, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   expiresIn,
		"role":         link.Role,
		"canvas_id":    link.CanvasID,
	})
}

// guestLimiter/phoneCrypto 由 canvasHandlers 携带(见 RegisterRoutes)。
