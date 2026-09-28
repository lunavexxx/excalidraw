package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
)

const contextKeyUserID = "authUserID"

// AuthRequired 校验 Authorization: Bearer <access token>,把用户 UUID 放进
// gin context;失败按全局契约返回 HTTP 200 + code 40100。
func AuthRequired(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
			c.Abort()
			return
		}
		userID, err := ParseAccessToken(jwtSecret, token)
		if err != nil {
			apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
			c.Abort()
			return
		}
		c.Set(contextKeyUserID, userID)
		c.Next()
	}
}

func userIDFrom(c *gin.Context) string {
	v, _ := c.Get(contextKeyUserID)
	s, _ := v.(string)
	return s
}
