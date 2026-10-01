package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

// guest token 面向匿名分享链接:持 share link 原文换取短期 guest JWT,
// 之后所有 guest 请求只携带 guest JWT(share link 原文不再出现在请求里)。
// sub 形如 "g:<shareLinkID>:<jti>",便于服务端按链接复核撤销/过期。

const (
	guestTokenTTL   = 12 * time.Hour
	guestSubPrefix  = "g:"
	guestRoleEditor = "editor"
	guestRoleViewer = "viewer"
)

type guestClaims struct {
	Typ      string `json:"typ"`
	CanvasID string `json:"cid"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// GuestInfo 是 guest token 解析后的稳定视图;ShareLinkID 供服务端按链接
// 复核有效性(撤销/过期即时生效,即使 JWT 尚未到期)。
type GuestInfo struct {
	Subject     string
	ShareLinkID string
	CanvasID    string
	Role        string
}

// SignGuestToken 签发绑定单个画布的 guest token;ttl 由调用方决定
// (默认 guestTokenTTL,签发入口可更短)。
func SignGuestToken(jwtSecret, shareLinkID, canvasID, role string, ttl time.Duration) (string, int, error) {
	if shareLinkID == "" || canvasID == "" {
		return "", 0, errors.New("guest token requires share link and canvas")
	}
	if role != guestRoleEditor && role != guestRoleViewer {
		return "", 0, fmt.Errorf("invalid guest role %q", role)
	}
	if ttl <= 0 {
		ttl = guestTokenTTL
	}
	jti := make([]byte, 12)
	if _, err := rand.Read(jti); err != nil {
		return "", 0, fmt.Errorf("read guest jti: %w", err)
	}
	now := time.Now()
	claims := guestClaims{
		Typ:      "guest",
		CanvasID: canvasID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   guestSubPrefix + shareLinkID + ":" + hex.EncodeToString(jti),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
	if err != nil {
		return "", 0, fmt.Errorf("sign guest token: %w", err)
	}
	return token, int(ttl.Seconds()), nil
}

func ParseGuestToken(jwtSecret, token string) (*GuestInfo, error) {
	parsed, err := jwt.ParseWithClaims(token, &guestClaims{}, func(t *jwt.Token) (any, error) {
		return []byte(jwtSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("parse guest token: %w", err)
	}
	claims, ok := parsed.Claims.(*guestClaims)
	if !ok || !parsed.Valid || claims.Typ != "guest" {
		return nil, errors.New("invalid guest token")
	}
	subject, shareLinkID, err := splitGuestSubject(claims.Subject)
	if err != nil {
		return nil, err
	}
	if claims.CanvasID == "" || (claims.Role != guestRoleEditor && claims.Role != guestRoleViewer) {
		return nil, errors.New("invalid guest claims")
	}
	return &GuestInfo{
		Subject:     subject,
		ShareLinkID: shareLinkID,
		CanvasID:    claims.CanvasID,
		Role:        claims.Role,
	}, nil
}

// splitGuestSubject 校验 sub 形如 "g:<shareLinkID>:<jti>" 并拆出链接 ID。
func splitGuestSubject(sub string) (subject, shareLinkID string, err error) {
	rest, ok := strings.CutPrefix(sub, guestSubPrefix)
	if !ok {
		return "", "", errors.New("malformed guest subject")
	}
	shareLinkID, jti, ok := strings.Cut(rest, ":")
	if !ok || shareLinkID == "" || jti == "" {
		return "", "", errors.New("malformed guest subject")
	}
	return sub, shareLinkID, nil
}

// Identity 是请求的稳定身份:登录用户(UserID 非空)或匿名 guest。
type Identity struct {
	UserID string
	Guest  *GuestInfo
}

// ParseIdentity 先按 access 解析,失败再按 guest;两者都不是则报错。
func ParseIdentity(jwtSecret, token string) (Identity, error) {
	if userID, err := ParseAccessToken(jwtSecret, token); err == nil {
		return Identity{UserID: userID}, nil
	}
	guest, err := ParseGuestToken(jwtSecret, token)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Guest: guest}, nil
}

// ToAccessIdentity 转成 store 层 ACL 输入。
func (i Identity) ToAccessIdentity() store.AccessIdentity {
	if i.Guest != nil {
		return store.AccessIdentity{
			Guest: &store.GuestIdentity{
				Subject:     i.Guest.Subject,
				ShareLinkID: i.Guest.ShareLinkID,
				CanvasID:    i.Guest.CanvasID,
				Role:        i.Guest.Role,
			},
		}
	}
	return store.AccessIdentity{UserID: i.UserID}
}
