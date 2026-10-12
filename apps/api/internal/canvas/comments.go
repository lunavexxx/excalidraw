package canvas

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

func registerCommentRoutes(g *gin.RouterGroup, h *canvasHandlers) {
	base := g.Group("/:id", func(c *gin.Context) {
		for _, name := range []string{"id", "threadId", "commentId"} {
			if value := c.Param(name); value != "" && !uuidRe.MatchString(value) {
				apiresp.Fail(c, apiresp.CodeCommentNotFound, "comment unavailable")
				c.Abort()
				return
			}
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		c.Next()
	}, h.authorize(store.RoleViewer))
	base.GET("/comment-threads", h.listCommentThreads)
	base.GET("/comment-pins", h.listCommentPins)
	base.GET("/comment-threads/:threadId", h.getCommentThread)
	write := base.Group("", auth.RequireUser(), func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && !h.commentLimiter.allow(auth.UserIDFrom(c)) {
			failComment(c, store.ErrCommentRateLimit)
			c.Abort()
			return
		}
		c.Next()
	})
	write.GET("/comment-mention-candidates", h.commentMentionCandidates)
	write.POST("/comment-threads", h.createCommentThread)
	write.POST("/comment-threads/read-all", h.readAllCommentThreads)
	write.PATCH("/comment-threads/:threadId", h.updateCommentThread)
	write.DELETE("/comment-threads/:threadId", h.deleteCommentThread)
	write.POST("/comment-threads/:threadId/read", h.readCommentThread)
	write.POST("/comment-threads/:threadId/comments", h.replyComment)
	write.PATCH("/comment-threads/:threadId/comments/:commentId", h.editComment)
	write.DELETE("/comment-threads/:threadId/comments/:commentId", h.deleteComment)
	write.PUT("/comment-threads/:threadId/comments/:commentId/reactions", h.addCommentReaction)
	write.DELETE("/comment-threads/:threadId/comments/:commentId/reactions", h.removeCommentReaction)
}
func failComment(c *gin.Context, err error) {
	code := apiresp.CodeInternal
	message := "comment action failed"
	switch {
	case errors.Is(err, store.ErrCommentInvalid):
		code = apiresp.CodeCommentInvalid
		message = err.Error()
	case errors.Is(err, store.ErrCommentNotFound):
		code = apiresp.CodeCommentNotFound
		message = err.Error()
	case errors.Is(err, store.ErrCommentForbidden):
		code = apiresp.CodeForbidden
		message = err.Error()
	case errors.Is(err, store.ErrCommentConflict):
		code = apiresp.CodeCommentConflict
		message = err.Error()
	case errors.Is(err, store.ErrCommentRateLimit):
		code = apiresp.CodeCommentRateLimit
		message = err.Error()
	}
	apiresp.Fail(c, code, message)
}
func (h *canvasHandlers) commentManagement(c *gin.Context) (bool, error) {
	caps, err := h.db.CanvasCapabilities(c.Request.Context(), canvasFrom(c), auth.UserIDFrom(c))
	return caps.ManageCollaborators, err
}
func (h *canvasHandlers) listCommentThreads(c *gin.Context) { h.commentList(c, 50) }
func (h *canvasHandlers) listCommentPins(c *gin.Context)    { h.commentList(c, 100) }
func (h *canvasHandlers) commentList(c *gin.Context, limit int) {
	manage, err := h.commentManagement(c)
	if err != nil {
		failComment(c, err)
		return
	}
	sort := c.DefaultQuery("sort", "date")
	resolved := c.DefaultQuery("resolved", "false")
	if resolved != "false" && resolved != "true" {
		failComment(c, store.ErrCommentInvalid)
		return
	}
	opts := store.CommentListOptions{Search: strings.TrimSpace(c.Query("search")), Sort: sort, IncludeResolved: resolved == "true", Cursor: c.Query("cursor"), Limit: limit}
	page, err := h.db.ListCommentThreads(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c), manage, opts)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, page)
}
func (h *canvasHandlers) getCommentThread(c *gin.Context) {
	before := int64(0)
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			failComment(c, store.ErrCommentInvalid)
			return
		}
	}
	manage, err := h.commentManagement(c)
	if err != nil {
		failComment(c, err)
		return
	}
	detail, err := h.db.GetCommentThread(c.Request.Context(), c.Param("id"), c.Param("threadId"), auth.UserIDFrom(c), manage, before)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, detail)
}
func (h *canvasHandlers) createCommentThread(c *gin.Context) { h.writeComment(c, "") }
func (h *canvasHandlers) replyComment(c *gin.Context)        { h.writeComment(c, c.Param("threadId")) }
func (h *canvasHandlers) writeComment(c *gin.Context, threadID string) {
	var req store.CommentInput
	if c.ShouldBindJSON(&req) != nil || !uuidRe.MatchString(req.RequestID) {
		failComment(c, store.ErrCommentInvalid)
		return
	}
	id, err := h.db.CreateComment(c.Request.Context(), c.Param("id"), threadID, auth.UserIDFrom(c), req)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, gin.H{"thread_id": id})
}
func (h *canvasHandlers) updateCommentThread(c *gin.Context) { h.writeCommentThread(c, false) }
func (h *canvasHandlers) deleteCommentThread(c *gin.Context) { h.writeCommentThread(c, true) }
func (h *canvasHandlers) writeCommentThread(c *gin.Context, remove bool) {
	var req store.CommentThreadUpdate
	if c.ShouldBindJSON(&req) != nil {
		failComment(c, store.ErrCommentInvalid)
		return
	}
	err := h.db.UpdateCommentThread(c.Request.Context(), c.Param("id"), c.Param("threadId"), auth.UserIDFrom(c), req, remove)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) editComment(c *gin.Context)   { h.writeExistingComment(c, false) }
func (h *canvasHandlers) deleteComment(c *gin.Context) { h.writeExistingComment(c, true) }
func (h *canvasHandlers) writeExistingComment(c *gin.Context, remove bool) {
	var req struct {
		Body     string                 `json:"body"`
		Mentions []store.CommentMention `json:"mentions"`
		Version  int64                  `json:"version"`
	}
	if c.ShouldBindJSON(&req) != nil {
		failComment(c, store.ErrCommentInvalid)
		return
	}
	err := h.db.UpdateComment(c.Request.Context(), c.Param("id"), c.Param("threadId"), c.Param("commentId"), auth.UserIDFrom(c), req.Body, req.Mentions, req.Version, remove)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) addCommentReaction(c *gin.Context)    { h.writeCommentReaction(c, false) }
func (h *canvasHandlers) removeCommentReaction(c *gin.Context) { h.writeCommentReaction(c, true) }
func (h *canvasHandlers) writeCommentReaction(c *gin.Context, remove bool) {
	var req struct {
		Emoji string `json:"emoji"`
	}
	if c.ShouldBindJSON(&req) != nil {
		failComment(c, store.ErrCommentInvalid)
		return
	}
	err := h.db.SetCommentReaction(c.Request.Context(), c.Param("id"), c.Param("threadId"), c.Param("commentId"), auth.UserIDFrom(c), req.Emoji, remove)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) readCommentThread(c *gin.Context) {
	var req struct {
		Version       int64 `json:"version"`
		UnreadVersion int64 `json:"unread_version"`
	}
	if c.ShouldBindJSON(&req) != nil {
		failComment(c, store.ErrCommentInvalid)
		return
	}
	err := h.db.ReadCommentThread(c.Request.Context(), c.Param("id"), c.Param("threadId"), auth.UserIDFrom(c), req.UnreadVersion, req.Version)
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) readAllCommentThreads(c *gin.Context) {
	err := h.db.ReadAllCommentThreads(c.Request.Context(), c.Param("id"), auth.UserIDFrom(c))
	if err != nil {
		failComment(c, err)
		return
	}
	apiresp.OK(c, nil)
}
func (h *canvasHandlers) commentMentionCandidates(c *gin.Context) {
	items, next, err := h.db.ListCanvasMembers(c.Request.Context(), canvasFrom(c), c.Query("cursor"), strings.TrimSpace(c.Query("search")), 50, false)
	if err != nil {
		failComment(c, err)
		return
	}
	authors := []store.CommentAuthor{}
	for _, m := range items {
		authors = append(authors, store.CommentAuthor{ID: m.UserID, Name: m.Nickname, Avatar: m.AvatarURL})
	}
	apiresp.OK(c, gin.H{"items": authors, "next_cursor": next})
}
