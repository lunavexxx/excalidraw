package canvas

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"strconv"
	"strings"
	"unicode/utf8"
)

func registerAccessRoutes(rg *gin.RouterGroup, h *canvasHandlers) {
	g := rg.Group("/canvases", auth.AuthRequired(h.jwtSecret))
	g.POST("/:id/invitations", h.authorizeManagement(), h.createInvitation)
	g.GET("/:id/invitations", h.authorizeManagement(), h.listInvitations)
	g.DELETE("/:id/invitations/:invitationId", h.authorizeManagement(), h.revokeInvitation)
	g.POST("/:id/access-entries", h.authorizeManagement(), h.createAccessEntry)
	g.GET("/:id/access-entries", h.authorizeManagement(), h.listAccessEntries)
	g.DELETE("/:id/access-entries/:entryId", h.authorizeManagement(), h.revokeAccessEntry)
	// Request-list access is granted only to readers or by a valid request-entry token.
	g.GET("/:id/access-requests", h.requestAccess, h.listAccessRequests)
	g.POST("/:id/access-requests", h.requestAccess, h.createAccessRequest)
	g.POST("/:id/access-requests/:requestId/decision", h.authorizeManagement(), h.decideAccessRequest)
	g.DELETE("/:id/access-requests/:requestId", h.requestAccess, h.cancelAccessRequest)
	user := rg.Group("", auth.AuthRequired(h.jwtSecret))
	user.GET("/invitations/preview", h.previewInvitation)
	user.GET("/invitations/:invitationId", h.previewOwnInvitation)
	user.POST("/invitations/:invitationId/accept", h.acceptOwnInvitation)
	user.POST("/invitations/accept", h.acceptInvitation)
	user.GET("/access-entries/preview", h.previewAccessEntry)
	user.GET("/notifications", h.listNotifications)
	user.POST("/notifications/read", h.readNotifications)
}
func failAccess(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrInviteIdentity):
		apiresp.Fail(c, apiresp.CodeForbidden, "invitation belongs to another account")
	case errors.Is(err, store.ErrAccessState):
		apiresp.Fail(c, apiresp.CodeShareLinkInvalid, "access action unavailable or already handled")
	default:
		apiresp.Fail(c, apiresp.CodeInternal, "access action failed")
	}
}
func validAccessRole(role string) bool { return role == "editor" || role == "viewer" }
func tokenHash(c *gin.Context, raw string) ([]byte, bool) {
	hash, err := hashShareLinkToken(raw)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeShareLinkInvalid, "invalid access link")
		return nil, false
	}
	return hash, true
}
func (h *canvasHandlers) createInvitation(c *gin.Context) {
	var req putCollaboratorRequest
	if c.ShouldBindJSON(&req) != nil || !validAccessRole(req.Role) {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "phone and editor/viewer role required")
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
	cipher, err := h.phoneCrypto.Seal(phone)
	if err != nil {
		failAccess(c, err)
		return
	}
	raw, hash, err := newShareLinkToken()
	if err != nil {
		failAccess(c, err)
		return
	}
	i, err := h.db.CreateInvitation(c.Request.Context(), c.Param("id"), req.Role, auth.UserIDFrom(c), auth.MaskPhone(phone), h.phoneCrypto.Hash(phone), cipher, hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"invitation": i, "token": raw})
}
func (h *canvasHandlers) listInvitations(c *gin.Context) {
	items, err := h.db.ListInvitations(c.Request.Context(), c.Param("id"))
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}
func (h *canvasHandlers) revokeInvitation(c *gin.Context) {
	if err := h.db.RevokeInvitation(c.Request.Context(), c.Param("id"), c.Param("invitationId")); err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) previewInvitation(c *gin.Context) {
	hash, ok := tokenHash(c, c.GetHeader("X-Access-Entry"))
	if !ok {
		return
	}
	i, err := h.db.InvitationByToken(c.Request.Context(), hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, i)
}
func (h *canvasHandlers) acceptInvitation(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if c.ShouldBindJSON(&req) != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "token required")
		return
	}
	hash, ok := tokenHash(c, req.Token)
	if !ok {
		return
	}
	id, err := h.db.AcceptInvitation(c.Request.Context(), auth.UserIDFrom(c), hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"canvas_id": id})
}
func (h *canvasHandlers) createAccessEntry(c *gin.Context) {
	raw, hash, err := newShareLinkToken()
	if err != nil {
		failAccess(c, err)
		return
	}
	e, err := h.db.CreateAccessEntry(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c), hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"entry": e, "token": raw})
}
func (h *canvasHandlers) listAccessEntries(c *gin.Context) {
	items, err := h.db.ListAccessEntries(c.Request.Context(), c.Param("id"))
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}
func (h *canvasHandlers) revokeAccessEntry(c *gin.Context) {
	if err := h.db.RevokeAccessEntry(c.Request.Context(), c.Param("id"), c.Param("entryId")); err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) previewAccessEntry(c *gin.Context) {
	hash, ok := tokenHash(c, c.GetHeader("X-Access-Entry"))
	if !ok {
		return
	}
	id, name, err := h.db.AccessEntryCanvas(c.Request.Context(), hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"canvas_id": id, "canvas_name": name})
}
func (h *canvasHandlers) requestAccess(c *gin.Context) {
	ident := auth.IdentityFrom(c)
	role, canvas, err := h.db.AuthorizeCanvas(c.Request.Context(), ident, c.Param("id"))
	if err == nil && role != store.RoleNone {
		c.Set(ctxKeyCanvas, canvas)
		c.Set(ctxKeyRole, string(role))
		c.Next()
		return
	}
	if err != nil && !errors.Is(err, store.ErrCanvasNotFound) {
		failAccess(c, err)
		c.Abort()
		return
	}
	hash, ok := tokenHash(c, c.GetHeader("X-Access-Entry"))
	if !ok {
		c.Abort()
		return
	}
	id, _, err := h.db.AccessEntryCanvas(c.Request.Context(), hash)
	if err != nil || id != c.Param("id") {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		c.Abort()
		return
	}
	c.Set(ctxKeyRole, "")
	c.Next()
}
func (h *canvasHandlers) listAccessRequests(c *gin.Context) {
	caps, err := h.db.CanvasCapabilities(c.Request.Context(), canvasFrom(c), auth.UserIDFrom(c))
	if err != nil {
		failAccess(c, err)
		return
	}
	items, err := h.db.ListAccessRequests(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c), caps.ReviewRequests)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}
func (h *canvasHandlers) createAccessRequest(c *gin.Context) {
	var req struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil || !validAccessRole(req.Role) || utf8.RuneCountInString(req.Reason) > 1000 {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "role or reason invalid")
		return
	}
	r, err := h.db.CreateAccessRequest(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c), req.Role, strings.TrimSpace(req.Reason))
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, r)
}
func (h *canvasHandlers) decideAccessRequest(c *gin.Context) {
	var req struct {
		Decision string `json:"decision"`
		Role     string `json:"role"`
		Reason   string `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Decision != "approved" && req.Decision != "rejected") || (req.Decision == "approved" && !validAccessRole(req.Role)) || utf8.RuneCountInString(req.Reason) > 1000 {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "decision invalid")
		return
	}
	if req.Decision == "rejected" {
		req.Role = ""
	}
	err := h.db.DecideAccessRequest(c.Request.Context(), c.Param("id"), c.Param("requestId"), auth.UserIDFrom(c), req.Decision, req.Role, strings.TrimSpace(req.Reason), true)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) cancelAccessRequest(c *gin.Context) {
	err := h.db.DecideAccessRequest(c.Request.Context(), c.Param("id"), c.Param("requestId"), auth.UserIDFrom(c), "cancelled", "", "", false)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) listNotifications(c *gin.Context) {
	var before int64
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid cursor")
			return
		}
	}
	items, unread, err := h.db.ListNotifications(c.Request.Context(), auth.UserIDFrom(c), before)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"items": items, "unread_count": unread})
}
func (h *canvasHandlers) readNotifications(c *gin.Context) {
	var req struct {
		ID int64 `json:"id"`
	}
	if c.ShouldBindJSON(&req) != nil || req.ID < 0 {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid id")
		return
	}
	if err := h.db.ReadNotifications(c.Request.Context(), auth.UserIDFrom(c), req.ID); err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, nil)
}

func (h *canvasHandlers) previewOwnInvitation(c *gin.Context) {
	hash, err := h.db.InvitationHashForUser(c.Request.Context(), c.Param("invitationId"), auth.UserIDFrom(c))
	if err != nil {
		failAccess(c, err)
		return
	}
	i, err := h.db.InvitationByToken(c.Request.Context(), hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, i)
}
func (h *canvasHandlers) acceptOwnInvitation(c *gin.Context) {
	hash, err := h.db.InvitationHashForUser(c.Request.Context(), c.Param("invitationId"), auth.UserIDFrom(c))
	if err != nil {
		failAccess(c, err)
		return
	}
	id, err := h.db.AcceptInvitation(c.Request.Context(), auth.UserIDFrom(c), hash)
	if err != nil {
		failAccess(c, err)
		return
	}
	apiresp.OK(c, gin.H{"canvas_id": id})
}
