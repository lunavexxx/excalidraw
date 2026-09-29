// Package apiresp 统一业务 API 响应包:HTTP 状态码恒为 200,
// 成功与错误全部由 body 的 code 表达(0=成功),前端/后续文档 API、
// room、admin 共用这一契约。
package apiresp

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// 业务错误码分段:400xx 参数/校验,401xx 鉴权,409xx 冲突,500xx 内部。
// 后续模块按前缀扩展(如文档 410xx)。
const (
	CodeOK                 = 0
	CodeInvalidRequest     = 40000
	CodeUnauthorized       = 40100
	CodeInvalidCredentials = 40101
	CodePhoneTaken         = 40900
	CodeInternal           = 50000

	// 41xxx: canvas 模块。
	CodeCanvasInvalid  = 41000
	CodeCanvasNotFound = 41001
	CodeCanvasConflict = 41002
	CodeCanvasTooLarge = 41003
)

type Body struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	Timestamp int64  `json:"timestamp"` // Unix 毫秒
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{
		Code:      CodeOK,
		Message:   "ok",
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	})
}

func Fail(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, Body{
		Code:      code,
		Message:   message,
		Data:      nil,
		Timestamp: time.Now().UnixMilli(),
	})
}
