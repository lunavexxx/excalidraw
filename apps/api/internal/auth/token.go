package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour
	refreshTokenLen = 32 // bytes
)

type accessClaims struct {
	Typ string `json:"typ"`
	jwt.RegisteredClaims
}

// SignAccessToken 签发 15 分钟 HS256 access token,sub 为用户 UUID。
func SignAccessToken(jwtSecret, userID string) (token string, expiresIn int, err error) {
	now := time.Now()
	claims := accessClaims{
		Typ: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenTTL)),
		},
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
	if err != nil {
		return "", 0, fmt.Errorf("sign access token: %w", err)
	}
	return token, int(accessTokenTTL.Seconds()), nil
}

// ParseAccessToken 校验签名与有效期,返回 sub(用户 UUID)。
func ParseAccessToken(jwtSecret, token string) (string, error) {
	parsed, err := jwt.ParseWithClaims(token, &accessClaims{}, func(t *jwt.Token) (any, error) {
		return []byte(jwtSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return "", fmt.Errorf("parse access token: %w", err)
	}
	claims, ok := parsed.Claims.(*accessClaims)
	if !ok || !parsed.Valid || claims.Typ != "access" || claims.Subject == "" {
		return "", errors.New("invalid access token")
	}
	return claims.Subject, nil
}

// NewRefreshToken 生成 32 字节随机令牌(base64url 给客户端)与其 sha256
// 摘要(入库;库中不存原文)。
func NewRefreshToken() (raw string, hash []byte, err error) {
	buf := make([]byte, refreshTokenLen)
	if _, err = rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("read refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256(buf)
	return raw, sum[:], nil
}

func HashRefreshToken(raw string) ([]byte, error) {
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(buf) != refreshTokenLen {
		return nil, errors.New("malformed refresh token")
	}
	sum := sha256.Sum256(buf)
	return sum[:], nil
}
