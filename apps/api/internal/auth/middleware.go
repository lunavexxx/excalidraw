package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

const (
	contextKeyUserID = "authUserID"
	contextKeyGuest  = "authGuest"
)

// AuthRequired 校验 Authorization: Bearer <access token>,把用户 UUID 放进
// gin context;失败按全局契约返回 HTTP 200 + code 40100。
func AuthRequired(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerFromHeader(c)
		if !ok {
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

// AuthAnyRequired 接受 access(登录用户)或 guest(分享链接)token;
// 供画布内容读取类路由使用(匿名 guest 只读)。
func AuthAnyRequired(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerFromHeader(c)
		if !ok {
			apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
			c.Abort()
			return
		}
		ident, err := ParseIdentity(jwtSecret, token)
		if err != nil {
			apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
			c.Abort()
			return
		}
		if ident.Guest != nil {
			c.Set(contextKeyGuest, *ident.Guest)
		} else {
			c.Set(contextKeyUserID, ident.UserID)
		}
		c.Next()
	}
}

// RequireUser 拒绝 guest(用户专属端点:列表/创建等);
// guest 已认证但不被允许,按 42002 处理。
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsGuest(c) {
			apiresp.Fail(c, apiresp.CodeForbidden, "guest not allowed")
			c.Abort()
			return
		}
		c.Next()
	}
}

func bearerFromHeader(c *gin.Context) (string, bool) {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func userIDFrom(c *gin.Context) string {
	v, _ := c.Get(contextKeyUserID)
	s, _ := v.(string)
	return s
}

// UserIDFrom 供其他模块(如 canvas)在 AuthRequired 之后取当前用户。
func UserIDFrom(c *gin.Context) string {
	return userIDFrom(c)
}

// GuestFrom 返回当前请求的 guest 身份(若为匿名链接访问)。
func GuestFrom(c *gin.Context) (GuestInfo, bool) {
	v, ok := c.Get(contextKeyGuest)
	g, ok2 := v.(GuestInfo)
	return g, ok && ok2
}

// IsGuest 判断当前请求是否匿名 guest。
func IsGuest(c *gin.Context) bool {
	_, ok := GuestFrom(c)
	return ok
}

// IdentityFrom 把 gin context 中的身份转为 store 层的 ACL 输入。
func IdentityFrom(c *gin.Context) store.AccessIdentity {
	if g, ok := GuestFrom(c); ok {
		return Identity{Guest: &g}.ToAccessIdentity()
	}
	return store.AccessIdentity{UserID: UserIDFrom(c)}
}
