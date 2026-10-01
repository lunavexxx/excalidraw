// Package canvas 实现画布管理 API(41xxx 错误码段)。
// 全部业务响应遵循 apiresp 契约(HTTP 200 + {code,message,data,timestamp});
// 唯一例外是文件内容 GET 返回原始二进制(immutable 缓存,内容 hash 寻址)。
//
// 权限模型(授权前置到 authorize 中间件):
//   - 内容操作按解析角色门控(viewer 读 / editor 写 / owner 全部);
//   - 信息类操作(改名/删除)一律 canvas owner;
//   - guest(分享链接)仅只读,任何写角色都被 42002 拒绝;
//   - 画布不存在或与身份无任何授权关系一律 41001(防枚举)。
package canvas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	scenepkg "github.com/lunavexxx/excalidraw/apps/api/internal/scene"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

// Limits 约束请求体大小,来自配置(Nacos/环境变量)。
type Limits struct {
	MaxSceneBytes      int // 单次场景快照上限
	MaxFileBytes       int // 单个文件字节上限
	MaxFilesPerRequest int // 单次批量上传文件数上限
	MaxThumbnailBytes  int // 缩略图 dataURL 上限
}

func DefaultLimits() Limits {
	return Limits{
		MaxSceneBytes:      2 << 20,
		MaxFileBytes:       4 << 20,
		MaxFilesPerRequest: 50,
		MaxThumbnailBytes:  64 << 10,
	}
}

const maxNameRunes = 100

// maxReadFoldEvents 读路径折叠窗口上限:pending 超限时宁可返回部分
// (cursor 如实反映实际折叠位置,余量由客户端 scene-diff 补齐)。
const maxReadFoldEvents = 2000

const (
	ctxKeyCanvas = "authzCanvas"
	ctxKeyRole   = "authzRole"
)

type canvasHandlers struct {
	db           *store.DB
	limits       Limits
	jwtSecret    string
	phoneCrypto  *auth.PhoneCrypto
	guestLimiter *ipRateLimiter
}

// RegisterRoutes 注册 /canvases 路由;需要 db 与 jwtSecret 已就绪。
// phoneCrypto 可为 nil(此时按手机号邀请协作者的端点回 50000)。
func RegisterRoutes(rg *gin.RouterGroup, db *store.DB, jwtSecret string, limits Limits, pc *auth.PhoneCrypto) {
	h := &canvasHandlers{
		db:           db,
		limits:       limits,
		jwtSecret:    jwtSecret,
		phoneCrypto:  pc,
		guestLimiter: newIPRateLimiter(10, time.Minute),
	}
	// 组级接受双身份(access/guest),用户专属端点再叠 RequireUser。
	g := rg.Group("/canvases", auth.AuthAnyRequired(jwtSecret))
	g.POST("", auth.RequireUser(), h.create)
	g.GET("", auth.RequireUser(), h.list)
	g.GET("/:id", h.authorize(store.RoleViewer), h.get)
	g.PUT("/:id/scene", h.authorize(store.RoleEditor), h.saveScene)
	g.PATCH("/:id", h.authorize(store.RoleOwner), h.rename)
	g.DELETE("/:id", h.authorize(store.RoleOwner), h.remove)
	g.PUT("/:id/files", h.authorize(store.RoleEditor), h.putFiles)
	g.GET("/:id/files/:fileId", h.authorize(store.RoleViewer), h.getFile)
	registerShareRoutes(g, h)

	// 匿名 guest 换票:唯一的无鉴权画布端点(持 URL 中的 share token)。
	rg.POST("/canvases/:id/access", h.guestAccess)
}

// authorize 解析当前身份对 :id 画布的角色并注入 context;
// minRole 为该路由要求的最低内容角色。
func (h *canvasHandlers) authorize(minRole store.CanvasRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		ident := auth.IdentityFrom(c)
		canvasID := c.Param("id")
		role, canvas, err := h.db.AuthorizeCanvas(c.Request.Context(), ident, canvasID)
		if errors.Is(err, store.ErrCanvasNotFound) || role == store.RoleNone {
			apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
			c.Abort()
			return
		}
		if err != nil {
			apiresp.Fail(c, apiresp.CodeInternal, "authorize failed")
			c.Abort()
			return
		}
		// 共享编辑仅及内容:guest 一律只读(HTTP 面;其房间内编辑经 room 落库)。
		if ident.Guest != nil && minRole != store.RoleViewer {
			apiresp.Fail(c, apiresp.CodeForbidden, "guest is read-only")
			c.Abort()
			return
		}
		if !role.AtLeast(minRole) {
			apiresp.Fail(c, apiresp.CodeForbidden, "insufficient role")
			c.Abort()
			return
		}
		c.Set(ctxKeyCanvas, canvas)
		c.Set(ctxKeyRole, string(role))
		c.Next()
	}
}

func canvasFrom(c *gin.Context) store.Canvas {
	v, _ := c.Get(ctxKeyCanvas)
	canvas, _ := v.(store.Canvas)
	return canvas
}

func roleFrom(c *gin.Context) string {
	v, _ := c.Get(ctxKeyRole)
	s, _ := v.(string)
	return s
}

type canvasResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	LatestVersion int64   `json:"latest_version"`
	Thumbnail     *string `json:"thumbnail"`
	LastOpenedAt  string  `json:"last_opened_at"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func canvasResponseFrom(c store.Canvas) canvasResponse {
	return canvasResponse{
		ID:            c.ID,
		Name:          c.Name,
		LatestVersion: c.LatestVersion,
		Thumbnail:     c.Thumbnail,
		LastOpenedAt:  c.LastOpenedAt.UTC().Format(time.RFC3339),
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type createRequest struct {
	Name string `json:"name"`
}

func (h *canvasHandlers) create(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// name 可省略;仅在传了非法 body 时拒绝。
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid request body")
		return
	}
	name, ok := normalizeCreateName(req.Name)
	if !ok {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "canvas name must be 1-100 characters")
		return
	}
	canvas, err := h.db.CreateCanvas(c.Request.Context(), auth.UserIDFrom(c), name)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "create canvas failed")
		return
	}
	apiresp.OK(c, gin.H{"canvas": canvasResponseFrom(canvas)})
}

func (h *canvasHandlers) list(c *gin.Context) {
	scope := c.Query("scope")
	if scope != "" && scope != "mine" && scope != "shared" {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "scope must be mine or shared")
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		n, err := parsePositiveInt(raw)
		if err != nil || n > 100 {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "limit must be 1-100")
			return
		}
		limit = n
	}
	cursor := c.Query("cursor")
	if cursor != "" {
		if _, _, err := store.DecodeCanvasCursor(cursor); err != nil {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid cursor")
			return
		}
	}
	listFn := h.db.ListCanvases
	if scope == "shared" {
		listFn = func(ctx context.Context, userID string, cursor string, limit int) ([]store.CanvasSummary, string, error) {
			return h.db.ListSharedCanvases(ctx, userID, cursor, limit)
		}
	}
	items, next, err := listFn(c.Request.Context(), auth.UserIDFrom(c), cursor, limit)
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

func (h *canvasHandlers) get(c *gin.Context) {
	canvas := canvasFrom(c)
	canvasID := canvas.ID
	// 读详情即"打开":刷新 last_opened_at(默认页"上次打开"依赖此字段)。
	if _, err := h.db.TouchLastOpened(c.Request.Context(), canvasID); err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load canvas failed")
		return
	}
	scene, err := h.db.GetCanvasScene(c.Request.Context(), canvasID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load scene failed")
		return
	}
	files, err := h.db.ListCanvasFiles(c.Request.Context(), canvasID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load file metas failed")
		return
	}
	var scenePayload any
	if len(scene.Data) > 0 || scene.FoldedUpto > 0 {
		data, cursor, err := h.effectiveSceneData(c.Request.Context(), canvasID, scene)
		if err != nil {
			apiresp.Fail(c, apiresp.CodeInternal, "load scene failed")
			return
		}
		if data != nil {
			// cursor = 读视图实际折叠到的最大事件 id(协议 v2 冷启动游标)。
			scenePayload = gin.H{"version": scene.Version, "data": json.RawMessage(data), "cursor": cursor}
		}
	}
	fileMetas := make([]gin.H, 0, len(files))
	for _, f := range files {
		fileMetas = append(fileMetas, gin.H{"file_id": f.FileID, "mime_type": f.MimeType})
	}
	apiresp.OK(c, gin.H{
		"canvas":  canvasResponseFrom(canvas),
		"scene":   scenePayload,
		"files":   fileMetas,
		"my_role": roleFrom(c),
	})
}

// effectiveSceneData 计算"唯一事实 + 待处理事件"的读视图:
// 快照 + 按 seq 折叠 pending 事件,不落库(compactor 负责持久化收敛)。
// 返回的 cursor 是读视图实际折叠到的最大事件 id(无 pending 时为
// folded_upto),供客户端作为 scene-diff 的起点。
func (h *canvasHandlers) effectiveSceneData(ctx context.Context, canvasID string, snapshot store.CanvasScene) ([]byte, int64, error) {
	events, err := h.db.ListPendingEvents(ctx, canvasID, snapshot.FoldedUpto, maxReadFoldEvents)
	if err != nil {
		return nil, 0, err
	}
	if len(events) == 0 {
		if len(snapshot.Data) == 0 {
			return nil, snapshot.FoldedUpto, nil
		}
		return snapshot.Data, snapshot.FoldedUpto, nil
	}
	baseElements, appState, err := scenepkg.SplitSceneData(snapshot.Data)
	if err != nil {
		return nil, 0, err
	}
	deltas := make([][]json.RawMessage, 0, len(events))
	for _, ev := range events {
		deltas = append(deltas, ev.Elements)
		if ev.AppState != nil {
			appState = ev.AppState
		}
	}
	folded := scenepkg.FoldElements(baseElements, deltas...)
	data, err := scenepkg.BuildSceneData(folded, appState)
	if err != nil {
		return nil, 0, err
	}
	return data, events[len(events)-1].ID, nil
}

type saveSceneRequest struct {
	Data        json.RawMessage `json:"data" binding:"required"`
	BaseVersion int64           `json:"base_version"`
	Thumbnail   *string         `json:"thumbnail"`
}

func (h *canvasHandlers) saveScene(c *gin.Context) {
	var req saveSceneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "data is required")
		return
	}
	if len(req.Data) > h.limits.MaxSceneBytes {
		apiresp.Fail(c, apiresp.CodeCanvasTooLarge, "scene too large")
		return
	}
	if !json.Valid(req.Data) {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "data must be valid json")
		return
	}
	if req.Thumbnail != nil && len(*req.Thumbnail) > h.limits.MaxThumbnailBytes {
		apiresp.Fail(c, apiresp.CodeCanvasTooLarge, "thumbnail too large")
		return
	}
	// 折叠写入:元素级版本即事实,base_version 不再作为闸门
	// (保留参数兼容旧客户端;多人并发保存天然无冲突)。
	elements, appState, err := scenepkg.SplitSceneData(req.Data)
	if err != nil || elements == nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "data must contain an elements array")
		return
	}
	if err != nil || elements == nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "data must contain an elements array")
		return
	}
	version, err := h.db.FoldCanvasScene(c.Request.Context(), c.Param("id"), elements, appState, req.Thumbnail)
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "save scene failed")
		return
	}
	apiresp.OK(c, gin.H{"version": version})
}

type renameRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *canvasHandlers) rename(c *gin.Context) {
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "name is required")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validName(req.Name) {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "canvas name must be 1-100 characters")
		return
	}
	canvas, err := h.db.RenameCanvas(c.Request.Context(), c.Param("id"), req.Name)
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "rename canvas failed")
		return
	}
	apiresp.OK(c, gin.H{"canvas": canvasResponseFrom(canvas)})
}

func (h *canvasHandlers) remove(c *gin.Context) {
	err := h.db.SoftDeleteCanvas(c.Request.Context(), c.Param("id"))
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "delete canvas failed")
		return
	}
	apiresp.OK(c, nil)
}

type filePayload struct {
	FileID   string `json:"file_id" binding:"required"`
	MimeType string `json:"mime_type" binding:"required"`
	Data     string `json:"data" binding:"required"`
}

type putFilesRequest struct {
	Files []filePayload `json:"files" binding:"required,dive"`
}

func (h *canvasHandlers) putFiles(c *gin.Context) {
	var req putFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "files are required")
		return
	}
	if len(req.Files) > h.limits.MaxFilesPerRequest {
		apiresp.Fail(c, apiresp.CodeCanvasTooLarge, "too many files per request")
		return
	}
	storeFiles := make([]store.CanvasFile, 0, len(req.Files))
	for _, f := range req.Files {
		if len(f.FileID) > 128 || strings.ContainsAny(f.FileID, "/\\") {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid file_id")
			return
		}
		if len(f.MimeType) > 100 {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "invalid mime_type")
			return
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			apiresp.Fail(c, apiresp.CodeCanvasInvalid, "file data must be base64")
			return
		}
		if len(data) > h.limits.MaxFileBytes {
			apiresp.Fail(c, apiresp.CodeCanvasTooLarge, "file too large")
			return
		}
		storeFiles = append(storeFiles, store.CanvasFile{FileID: f.FileID, MimeType: f.MimeType, Data: data})
	}
	err := h.db.UpsertCanvasFiles(c.Request.Context(), c.Param("id"), storeFiles)
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "save files failed")
		return
	}
	apiresp.OK(c, gin.H{"saved": len(storeFiles)})
}

// getFile 是 apiresp 契约的唯一例外:返回原始二进制以便浏览器缓存
// (file_id 为内容 hash,响应 immutable);找不到仍按契约回 JSON 错误。
func (h *canvasHandlers) getFile(c *gin.Context) {
	file, err := h.db.GetCanvasFile(c.Request.Context(), c.Param("id"), c.Param("fileId"))
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "file not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load file failed")
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(200, file.MimeType, file.Data)
}

func validName(name string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	return n >= 1 && n <= maxNameRunes
}

// normalizeCreateName 归一化创建名:空白视为省略(空串,store 层填默认名);
// 超长拒绝。
func normalizeCreateName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", false
	}
	return name, true
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
