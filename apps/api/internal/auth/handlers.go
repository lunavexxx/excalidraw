package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

// bearerToken 取 Authorization: Bearer <token>;无头或格式不对返回空。
// access 路由(经 AuthRequired)与 refresh/logout(回传 refresh token)共用。
func bearerToken(c *gin.Context) string {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

type userResponse struct {
	ID          string `json:"id"`
	Nickname    string `json:"nickname"`
	PhoneMasked string `json:"phone_masked"`
	CreatedAt   string `json:"created_at"`
}

// userResponseFrom 只暴露脱敏手机号,明文/密文/哈希永不出 API。
func userResponseFrom(u store.User) userResponse {
	return userResponse{
		ID:          u.ID,
		Nickname:    u.Nickname,
		PhoneMasked: u.PhoneMasked,
		CreatedAt:   u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type registerRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Password string `json:"password" binding:"required"`
	Nickname string `json:"nickname"`
}

type loginRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type authHandlers struct {
	db          *store.DB
	jwtSecret   string
	phoneCrypto *PhoneCrypto
}

func RegisterRoutes(rg *gin.RouterGroup, db *store.DB, jwtSecret string, pc *PhoneCrypto) {
	h := &authHandlers{db: db, jwtSecret: jwtSecret, phoneCrypto: pc}
	rg.POST("/auth/register", h.register)
	rg.POST("/auth/login", h.login)
	rg.POST("/auth/refresh", h.refresh)
	rg.POST("/auth/logout", h.logout)
	rg.GET("/me", AuthRequired(jwtSecret), h.me)
}

func (h *authHandlers) register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeInvalidRequest, "phone and password are required")
		return
	}
	phone, err := NormalizePhone(req.Phone)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInvalidRequest, "invalid phone")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		apiresp.Fail(c, apiresp.CodeInvalidRequest, "password must be 8-72 characters")
		return
	}
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = RandomNickname()
	}
	if len([]rune(nickname)) > 20 {
		apiresp.Fail(c, apiresp.CodeInvalidRequest, "nickname too long")
		return
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "hash password failed")
		return
	}
	cipher, err := h.phoneCrypto.Seal(phone)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "seal phone failed")
		return
	}
	user, err := h.db.CreateUser(c.Request.Context(), store.NewUser{
		PhoneCipher:  cipher,
		PhoneHash:    h.phoneCrypto.Hash(phone),
		PhoneMasked:  MaskPhone(phone),
		CountryCode:  "+86",
		PasswordHash: []byte(passwordHash),
		Nickname:     nickname,
	})
	if errors.Is(err, store.ErrPhoneTaken) {
		apiresp.Fail(c, apiresp.CodePhoneTaken, "phone already registered")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "create user failed")
		return
	}
	h.issueSession(c, user)
}

func (h *authHandlers) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.CodeInvalidRequest, "phone and password are required")
		return
	}
	phone, err := NormalizePhone(req.Phone)
	if err != nil {
		// 与"用户不存在"合并成同一错误,不暴露号码是否已注册。
		apiresp.Fail(c, apiresp.CodeInvalidCredentials, "wrong phone or password")
		return
	}
	user, err := h.db.GetUserByPhoneHash(c.Request.Context(), h.phoneCrypto.Hash(phone))
	if err != nil || !VerifyPassword(string(user.PasswordHash), req.Password) {
		apiresp.Fail(c, apiresp.CodeInvalidCredentials, "wrong phone or password")
		return
	}
	h.issueSession(c, user)
}

func (h *authHandlers) refresh(c *gin.Context) {
	raw := bearerToken(c)
	if raw == "" {
		apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
		return
	}
	oldHash, err := HashRefreshToken(raw)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeUnauthorized, "authentication required")
		return
	}
	newRaw, newHash, err := NewRefreshToken()
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "issue refresh token failed")
		return
	}
	userID, err := h.db.RotateRefreshToken(c.Request.Context(), oldHash, newHash, time.Now().Add(refreshTokenTTL))
	if errors.Is(err, store.ErrRefreshTokenInvalid) {
		apiresp.Fail(c, apiresp.CodeUnauthorized, "refresh token invalid")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "rotate refresh token failed")
		return
	}
	user, err := h.db.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load user failed")
		return
	}
	h.writeTokenResponse(c, user, newRaw)
}

func (h *authHandlers) logout(c *gin.Context) {
	if raw := bearerToken(c); raw != "" {
		if hash, err := HashRefreshToken(raw); err == nil {
			_ = h.db.RevokeRefreshToken(c.Request.Context(), hash)
		}
	}
	apiresp.OK(c, nil)
}

func (h *authHandlers) me(c *gin.Context) {
	user, err := h.db.GetUserByID(c.Request.Context(), userIDFrom(c))
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "load user failed")
		return
	}
	apiresp.OK(c, gin.H{"user": userResponseFrom(user)})
}

// issueSession 签发 refresh token + access token,register 与 login 共用;
// refresh token 经响应体返回,由客户端存储并随 Authorization 头回传。
func (h *authHandlers) issueSession(c *gin.Context, user store.User) {
	raw, hash, err := NewRefreshToken()
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "issue refresh token failed")
		return
	}
	if err := h.db.CreateRefreshToken(c.Request.Context(), user.ID, hash, time.Now().Add(refreshTokenTTL)); err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "store refresh token failed")
		return
	}
	h.writeTokenResponse(c, user, raw)
}

func (h *authHandlers) writeTokenResponse(c *gin.Context, user store.User, refreshToken string) {
	token, expiresIn, err := SignAccessToken(h.jwtSecret, user.ID)
	if err != nil {
		apiresp.Fail(c, apiresp.CodeInternal, "sign access token failed")
		return
	}
	apiresp.OK(c, gin.H{
		"user":          userResponseFrom(user),
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
		"refresh_token": refreshToken,
	})
}
