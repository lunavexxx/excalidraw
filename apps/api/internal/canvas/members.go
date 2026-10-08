package canvas

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"strings"
)

func (h *canvasHandlers) authorizeManagement() gin.HandlerFunc {
	return func(c *gin.Context) {
		ident := auth.IdentityFrom(c)
		role, canvas, err := h.db.AuthorizeCanvas(c.Request.Context(), ident, c.Param("id"))
		if errors.Is(err, store.ErrCanvasNotFound) || (err == nil && role == store.RoleNone) {
			apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
			c.Abort()
			return
		}
		if err != nil {
			apiresp.Fail(c, apiresp.CodeInternal, "authorize failed")
			c.Abort()
			return
		}
		caps, err := h.db.CanvasCapabilities(c.Request.Context(), canvas, ident.UserID)
		if err != nil {
			apiresp.Fail(c, apiresp.CodeInternal, "authorize failed")
			c.Abort()
			return
		}
		if ident.Guest != nil || !caps.ManageCollaborators {
			apiresp.Fail(c, apiresp.CodeForbidden, "management permission required")
			c.Abort()
			return
		}
		c.Set(ctxKeyCanvas, canvas)
		c.Set(ctxKeyRole, string(role))
		c.Next()
	}
}

func (h *canvasHandlers) listMembers(c *gin.Context) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := parsePositiveInt(raw)
		if err != nil || n > 100 {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "limit must be 1-100")
			return
		}
		limit = n
	}
	caps, err := h.db.CanvasCapabilities(c.Request.Context(), canvasFrom(c), auth.UserIDFrom(c))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load capabilities failed")
		return
	}
	items, next, err := h.db.ListCanvasMembers(c.Request.Context(), canvasFrom(c), c.Query("cursor"), strings.TrimSpace(c.Query("search")), limit, caps.ManageCollaborators)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load members failed")
		return
	}
	apiresp.OK(c, gin.H{"items": items, "next_cursor": next, "capabilities": caps})
}
