// Package workspace 实现工作区管理 API(42xxx 错误码段)。
// 工作区承载团队级内容角色(owner/admin/editor → 内容 editor,
// viewer → 内容 viewer);画布信息类操作不在此层(仍归 canvas owner)。
package workspace

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

const maxNameRunes = 100

type handlers struct {
	db          *store.DB
	jwtSecret   string
	phoneCrypto *auth.PhoneCrypto
}

// RegisterRoutes 注册 /workspaces 路由;需要 db、jwtSecret 与 phoneCrypto。
func RegisterRoutes(rg *gin.RouterGroup, db *store.DB, jwtSecret string, pc *auth.PhoneCrypto) {
	h := &handlers{db: db, jwtSecret: jwtSecret, phoneCrypto: pc}
	g := rg.Group("/workspaces", auth.AuthRequired(jwtSecret))
	g.GET("", h.listMine)
	g.POST("", h.create)
	g.GET("/:id", h.requireRole("viewer"), h.detail)
	g.PATCH("/:id", h.requireRole("admin"), h.rename)
	g.GET("/:id/canvases", h.requireRole("viewer"), h.listCanvases)
	g.POST("/:id/members", h.requireRole("admin"), h.addMember)
	g.PATCH("/:id/members/:userId", h.requireRole("admin"), h.updateMember)
	g.DELETE("/:id/members/:userId", h.requireRole("admin"), h.removeMember)
}

// requireRole 校验当前用户在工作区中的角色门槛;非成员按 42001 处理
// (对非成员不暴露工作区存在性)。
func (h *handlers) requireRole(min string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, err := h.db.WorkspaceRoleOf(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c))
		if err != nil {
			apiresp.Fail(c, apiresp.CodeInternal, "resolve role failed")
			c.Abort()
			return
		}
		if role == "" {
			apiresp.Fail(c, apiresp.CodeWorkspaceNotFound, "workspace not found")
			c.Abort()
			return
		}
		if !store.WorkspaceRoleAtLeast(role, min) {
			apiresp.Fail(c, apiresp.CodeForbidden, "insufficient role")
			c.Abort()
			return
		}
		c.Next()
	}
}

type workspaceResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	Personal  bool   `json:"is_personal"`
	MyRole    string `json:"my_role,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func workspaceResponseFrom(w store.Workspace) workspaceResponse {
	return workspaceResponse{
		ID:        w.ID,
		Name:      w.Name,
		OwnerID:   w.OwnerID,
		Personal:  w.IsPersonal,
		CreatedAt: w.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: w.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *handlers) listMine(c *gin.Context) {
	items, err := h.db.ListUserWorkspaces(c.Request.Context(), auth.UserIDFrom(c))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "list workspaces failed")
		return
	}
	resp := make([]workspaceResponse, 0, len(items))
	for _, w := range items {
		r := workspaceResponseFrom(w.Workspace)
		r.MyRole = w.MyRole
		resp = append(resp, r)
	}
	apiresp.OK(c, gin.H{"items": resp})
}

type createRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *handlers) create(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "name is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if !validName(name) {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "workspace name must be 1-100 characters")
		return
	}
	w, err := h.db.CreateTeamWorkspace(c.Request.Context(), auth.UserIDFrom(c), name)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "create workspace failed")
		return
	}
	apiresp.OK(c, gin.H{"workspace": workspaceResponseFrom(w)})
}

type detailResponse struct {
	Workspace workspaceResponse `json:"workspace"`
	Members   []memberResponse  `json:"members"`
}

type memberResponse struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	PhoneMasked string `json:"phone_masked"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at"`
}

func memberResponseFrom(m store.WorkspaceMember) memberResponse {
	return memberResponse{
		UserID:      m.UserID,
		Nickname:    m.Nickname,
		PhoneMasked: m.PhoneMasked,
		Role:        m.Role,
		CreatedAt:   m.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *handlers) detail(c *gin.Context) {
	w, err := h.db.GetWorkspace(c.Request.Context(), c.Param("id"))
	if errors.Is(err, store.ErrWorkspaceNotFound) {
		apiresp.Fail(c, apiresp.CodeWorkspaceNotFound, "workspace not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load workspace failed")
		return
	}
	members, err := h.db.ListWorkspaceMembers(c.Request.Context(), w.ID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load members failed")
		return
	}
	respMembers := make([]memberResponse, 0, len(members))
	for _, m := range members {
		respMembers = append(respMembers, memberResponseFrom(m))
	}
	resp := detailResponse{Workspace: workspaceResponseFrom(w), Members: respMembers}
	// personal 工作区对成员展示自己的角色,便于前端判定。
	if role, err := h.db.WorkspaceRoleOf(c.Request.Context(), w.ID, auth.UserIDFrom(c)); err == nil {
		resp.Workspace.MyRole = role
	}
	apiresp.OK(c, resp)
}

type renameRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *handlers) rename(c *gin.Context) {
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "name is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if !validName(name) {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "workspace name must be 1-100 characters")
		return
	}
	w, err := h.db.RenameWorkspace(c.Request.Context(), c.Param("id"), name)
	if errors.Is(err, store.ErrWorkspaceNotFound) {
		apiresp.Fail(c, apiresp.CodeWorkspaceNotFound, "workspace not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "rename workspace failed")
		return
	}
	apiresp.OK(c, gin.H{"workspace": workspaceResponseFrom(w)})
}

func (h *handlers) listCanvases(c *gin.Context) {
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		n, err := parsePositiveInt(raw)
		if err != nil || n > 100 {
			apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "limit must be 1-100")
			return
		}
		limit = n
	}
	cursor := c.Query("cursor")
	if cursor != "" {
		if _, _, err := store.DecodeCanvasCursor(cursor); err != nil {
			apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "invalid cursor")
			return
		}
	}
	items, next, err := h.db.ListWorkspaceCanvases(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c), cursor, limit)
	if errors.Is(err, store.ErrWorkspaceNotFound) {
		apiresp.Fail(c, apiresp.CodeWorkspaceNotFound, "workspace not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "list canvases failed")
		return
	}
	respItems := make([]gin.H, 0, len(items))
	for _, s := range items {
		respItems = append(respItems, gin.H{
			"id":             s.ID,
			"name":           s.Name,
			"thumbnail":      s.Thumbnail,
			"latest_version": s.LatestVersion,
			"last_opened_at": s.LastOpenedAt.UTC().Format(time.RFC3339),
		})
	}
	apiresp.OK(c, gin.H{"items": respItems, "next_cursor": next})
}

// validWorkspaceRole 可被授予的成员角色(owner 仅随创建产生)。
func validWorkspaceRole(role string) bool {
	return role == "admin" || role == "editor" || role == "viewer"
}

type addMemberRequest struct {
	Phone string `json:"phone" binding:"required"`
	Role  string `json:"role" binding:"required"`
}

func (h *handlers) addMember(c *gin.Context) {
	var req addMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "phone and role are required")
		return
	}
	if !validWorkspaceRole(req.Role) {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "role must be admin, editor or viewer")
		return
	}
	phone, err := auth.NormalizePhone(req.Phone)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "invalid phone")
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
	if err := h.db.AddMember(c.Request.Context(), c.Param("id"), target.ID, req.Role); errors.Is(err, store.ErrAlreadyMember) {
		apiresp.Fail(c, apiresp.CodeAlreadyMember, "already a member")
		return
	} else if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "add member failed")
		return
	}
	apiresp.OK(c, gin.H{"member": memberResponseFrom(store.WorkspaceMember{
		WorkspaceID: c.Param("id"),
		UserID:      target.ID,
		Role:        req.Role,
		Nickname:    target.Nickname,
		PhoneMasked: target.PhoneMasked,
	})})
}

type updateMemberRequest struct {
	Role string `json:"role" binding:"required"`
}

func (h *handlers) updateMember(c *gin.Context) {
	var req updateMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "role is required")
		return
	}
	if !validWorkspaceRole(req.Role) {
		apiresp.Fail(c, apiresp.CodeWorkspaceInvalid, "role must be admin, editor or viewer")
		return
	}
	err := h.db.UpdateMemberRole(c.Request.Context(), c.Param("id"), c.Param("userId"), req.Role)
	switch {
	case errors.Is(err, store.ErrCannotModifyOwner):
		apiresp.Fail(c, apiresp.CodeForbidden, "cannot modify workspace owner")
	case errors.Is(err, store.ErrMemberNotFound):
		apiresp.Fail(c, apiresp.CodeWorkspaceNotFound, "member not found")
	case err != nil:
		apiresp.Fail(c, apiresp.CodeInternal, "update member failed")
	default:
		apiresp.OK(c, gin.H{"user_id": c.Param("userId"), "role": req.Role})
	}
}

func (h *handlers) removeMember(c *gin.Context) {
	err := h.db.RemoveMember(c.Request.Context(), c.Param("id"), c.Param("userId"))
	switch {
	case errors.Is(err, store.ErrCannotModifyOwner):
		apiresp.Fail(c, apiresp.CodeForbidden, "cannot remove workspace owner")
	case errors.Is(err, store.ErrMemberNotFound):
		apiresp.Fail(c, apiresp.CodeWorkspaceNotFound, "member not found")
	case err != nil:
		apiresp.Fail(c, apiresp.CodeInternal, "remove member failed")
	default:
		apiresp.OK(c, nil)
	}
}

func validName(name string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	return n >= 1 && n <= maxNameRunes
}

func parsePositiveInt(raw string) (int, error) {
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(ch-'0')
		if n > 1<<31 {
			return 0, errors.New("too large")
		}
	}
	if n <= 0 {
		return 0, errors.New("must be positive")
	}
	return n, nil
}
