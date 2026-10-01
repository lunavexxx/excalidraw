// 握手鉴权:验签 JWT(HS256,与 apps/api 共享密钥),区分 access(登录)
// 与 guest(分享链接)两种身份;无效即拒绝连接(fail-closed)。
// claim 形状与 apps/api/internal/auth 对齐(该包跨模块不可导入,此处
// 复制解析并用同一组表测试用例锁住,防两处漂移)。
package main

import (
	"errors"
	"strings"
	"sync"

	"github.com/golang-jwt/jwt/v5"
)

const guestSubPrefix = "g:"

type GuestIdentity struct {
	Subject     string
	ShareLinkID string
	CanvasID    string
	Role        string // "editor" | "viewer"
}

type aclEntry struct {
	role    string // "owner"|"editor"|"viewer",或 ""(已缓存的拒绝)
	expires int64  // epoch 秒
}

// Session 是握手成功后的连接级身份,挂到 socket.Data() 上。
type Session struct {
	Token    string
	TokenExp int64 // epoch 秒;无 exp 则 0(ACL 缓存 TTL 回落 60s)
	UserID   string
	Guest    *GuestIdentity

	mu    sync.Mutex
	acl   map[string]*aclEntry // canvasId → 角色缓存
	roles map[string]string    // canvasId → join 后的内容角色
}

// setRole 记录 join 成功后该画布房间的角色;按房间存储——单连接加入
// 多房间时,viewer 强制逐房间生效(比 Node 版单值 last-write-wins 更严)。
func (s *Session) setRole(canvasID, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		s.roles = make(map[string]string)
	}
	s.roles[canvasID] = role
}

func (s *Session) roleOf(canvasID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.roles[canvasID]
}

func (s *Session) Identity() string {
	if s.UserID != "" {
		return s.UserID
	}
	return s.Guest.Subject
}

func (s *Session) cachedACL(canvasID string, now int64) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acl == nil {
		return "", false
	}
	e, ok := s.acl[canvasID]
	if !ok || e.expires <= now {
		return "", false
	}
	return e.role, true
}

func (s *Session) storeACL(canvasID, role string, expires int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acl == nil {
		s.acl = make(map[string]*aclEntry)
	}
	s.acl[canvasID] = &aclEntry{role: role, expires: expires}
}

// Authenticate 验签并归类身份;返回 nil 表示拒绝连接。
func Authenticate(jwtSecret, token string) *Session {
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		return []byte(jwtSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		return nil
	}
	typ, _ := claims["typ"].(string)
	sub, _ := claims["sub"].(string)
	var exp int64
	if v, ok := claims["exp"].(float64); ok {
		exp = int64(v)
	}

	if typ == "access" && sub != "" {
		return &Session{Token: token, TokenExp: exp, UserID: sub}
	}
	if typ == "guest" && sub != "" {
		cid, _ := claims["cid"].(string)
		role, _ := claims["role"].(string)
		if cid == "" || (role != "editor" && role != "viewer") {
			return nil
		}
		shareLinkID, err := parseGuestSubject(sub)
		if err != nil {
			return nil
		}
		return &Session{Token: token, TokenExp: exp, Guest: &GuestIdentity{
			Subject:     sub,
			ShareLinkID: shareLinkID,
			CanvasID:    cid,
			Role:        role,
		}}
	}
	return nil
}

// parseGuestSubject 校验 sub 形如 "g:<shareLinkID>:<jti>" 并拆出链接 ID。
func parseGuestSubject(sub string) (string, error) {
	rest, ok := strings.CutPrefix(sub, guestSubPrefix)
	if !ok {
		return "", errors.New("malformed guest subject")
	}
	shareLinkID, jti, ok := strings.Cut(rest, ":")
	if !ok || shareLinkID == "" || jti == "" {
		return "", errors.New("malformed guest subject")
	}
	return shareLinkID, nil
}
