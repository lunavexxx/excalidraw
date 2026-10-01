// internal_access.go:room 服务的内部回调端点(容器内网,Caddy 侧显式 403 兜底)。
// 调用方式统一携带:
//
//	X-Internal-Token: <与服务端 .env/Nacos 同源的内部令牌>
//	Authorization: Bearer <用户 access token 或 guest token>(仅 ACL 端点需要)
//
// 协议 v2 新增 scene-diff:状态补丁(Sync Step 2)由 API 从共享事件流计算。
package canvas

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	scenepkg "github.com/lunavexxx/excalidraw/apps/api/internal/scene"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

// RegisterInternalRoutes 注册 /internal 路由组;internalToken 为空时
// 不注册(依赖方 fail-closed)。
func RegisterInternalRoutes(rg *gin.RouterGroup, db *store.DB, jwtSecret, internalToken string) {
	if internalToken == "" {
		return
	}
	h := &canvasHandlers{db: db, jwtSecret: jwtSecret}
	g := rg.Group("/internal", func(c *gin.Context) {
		if c.GetHeader("X-Internal-Token") != internalToken {
			apiresp.Fail(c, apiresp.CodeUnauthorized, "internal token mismatch")
			c.Abort()
			return
		}
		c.Next()
	})
	g.GET("/canvas/:id/access", h.internalAccess)
	g.GET("/canvas/:id/scene-diff", h.internalSceneDiff)
}

func (h *canvasHandlers) internalAccess(c *gin.Context) {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
		return
	}
	ident, err := auth.ParseIdentity(h.jwtSecret, strings.TrimSpace(token))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
		return
	}
	role, _, err := h.db.AuthorizeCanvas(c.Request.Context(), ident.ToAccessIdentity(), c.Param("id"))
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "authorize failed")
		return
	}

	resp := gin.H{"role": string(role), "guest": ident.Guest != nil}
	if ident.Guest != nil {
		resp["guest_role"] = ident.Guest.Role
	} else if role != store.RoleNone {
		resp["user_id"] = ident.UserID
		if user, err := h.db.GetUserByID(c.Request.Context(), ident.UserID); err == nil {
			resp["display_name"] = user.Nickname
		}
	}
	apiresp.OK(c, resp)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// internalSceneDiff 返回 after(seq)之后的事件折叠补丁(协议 v2 Sync Step 2):
// 只在本批事件之间互相 FoldElements(客户端本地状态即基底,快照不参与),
// cursor 为本批实际折叠到的最大事件 id,has_more 表示还有剩余、需继续拉取。
// room 收到客户端的 sync-request 后转发到这里。
func (h *canvasHandlers) internalSceneDiff(c *gin.Context) {
	canvasID := c.Param("id")
	if !uuidRe.MatchString(canvasID) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	after, err := strconv.ParseInt(c.Query("after"), 10, 64)
	if err != nil || after < 0 {
		after = 0
	}
	limit := maxReadFoldEvents
	if raw := c.Query("limit"); raw != "" {
		n, err := parsePositiveInt(raw)
		if err != nil || n > maxReadFoldEvents {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "limit must be 1-2000")
			return
		}
		limit = n
	}

	if _, err := h.db.GetCanvasMetaOnly(c.Request.Context(), canvasID); errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	} else if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load canvas failed")
		return
	}

	events, err := h.db.ListPendingEvents(c.Request.Context(), canvasID, after, limit)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load events failed")
		return
	}
	deltas := make([][]json.RawMessage, 0, len(events))
	cursor := after
	for _, ev := range events {
		deltas = append(deltas, ev.Elements)
		if ev.ID > cursor {
			cursor = ev.ID
		}
	}
	folded := scenepkg.FoldElements(nil, deltas...)
	apiresp.OK(c, gin.H{
		"elements": folded,
		"cursor":   cursor,
		"has_more": len(events) == limit,
	})
}
