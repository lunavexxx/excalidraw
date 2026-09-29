// Package canvas 实现画布管理 API(41xxx 错误码段)。
// 全部业务响应遵循 apiresp 契约(HTTP 200 + {code,message,data,timestamp});
// 唯一例外是文件内容 GET 返回原始二进制(immutable 缓存,内容 hash 寻址)。
package canvas

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
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

type canvasHandlers struct {
	db     *store.DB
	limits Limits
}

// RegisterRoutes 注册 /canvases 路由;需要 db 与 jwtSecret 已就绪。
func RegisterRoutes(rg *gin.RouterGroup, db *store.DB, jwtSecret string, limits Limits) {
	h := &canvasHandlers{db: db, limits: limits}
	g := rg.Group("/canvases", auth.AuthRequired(jwtSecret))
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PUT("/:id/scene", h.saveScene)
	g.PATCH("/:id", h.rename)
	g.DELETE("/:id", h.remove)
	g.PUT("/:id/files", h.putFiles)
	g.GET("/:id/files/:fileId", h.getFile)
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
	items, next, err := h.db.ListCanvases(c.Request.Context(), auth.UserIDFrom(c), cursor, limit)
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
	userID := auth.UserIDFrom(c)
	canvasID := c.Param("id")
	canvas, err := h.db.GetCanvas(c.Request.Context(), userID, canvasID)
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load canvas failed")
		return
	}
	scene, err := h.db.GetCanvasScene(c.Request.Context(), userID, canvasID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load scene failed")
		return
	}
	files, err := h.db.ListCanvasFiles(c.Request.Context(), userID, canvasID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load file metas failed")
		return
	}
	var scenePayload any
	if len(scene.Data) > 0 {
		scenePayload = gin.H{"version": scene.Version, "data": json.RawMessage(scene.Data)}
	}
	fileMetas := make([]gin.H, 0, len(files))
	for _, f := range files {
		fileMetas = append(fileMetas, gin.H{"file_id": f.FileID, "mime_type": f.MimeType})
	}
	apiresp.OK(c, gin.H{
		"canvas": canvasResponseFrom(canvas),
		"scene":  scenePayload,
		"files":  fileMetas,
	})
}

type saveSceneRequest struct {
	Data        json.RawMessage `json:"data" binding:"required"`
	BaseVersion int64           `json:"base_version"`
	Thumbnail   *string         `json:"thumbnail"`
}

func (h *canvasHandlers) saveScene(c *gin.Context) {
	var req saveSceneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "data and base_version are required")
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
	if req.BaseVersion < 0 {
		apiresp.Fail(c, apiresp.CodeCanvasInvalid, "base_version must be >= 0")
		return
	}
	if req.Thumbnail != nil && len(*req.Thumbnail) > h.limits.MaxThumbnailBytes {
		apiresp.Fail(c, apiresp.CodeCanvasTooLarge, "thumbnail too large")
		return
	}
	version, err := h.db.SaveCanvasScene(c.Request.Context(), auth.UserIDFrom(c), c.Param("id"), req.BaseVersion, req.Data, req.Thumbnail)
	if errors.Is(err, store.ErrCanvasNotFound) {
		apiresp.Fail(c, apiresp.CodeCanvasNotFound, "canvas not found")
		return
	}
	if errors.Is(err, store.ErrCanvasVersionConflict) {
		apiresp.Fail(c, apiresp.CodeCanvasConflict, "version conflict")
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
	canvas, err := h.db.RenameCanvas(c.Request.Context(), auth.UserIDFrom(c), c.Param("id"), req.Name)
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
	err := h.db.SoftDeleteCanvas(c.Request.Context(), auth.UserIDFrom(c), c.Param("id"))
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
	err := h.db.UpsertCanvasFiles(c.Request.Context(), auth.UserIDFrom(c), c.Param("id"), storeFiles)
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
	file, err := h.db.GetCanvasFile(c.Request.Context(), auth.UserIDFrom(c), c.Param("id"), c.Param("fileId"))
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
