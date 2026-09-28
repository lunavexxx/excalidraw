package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// 密码只存 bcrypt 不可逆哈希。bcrypt 输入上限 72 字节,超长在 handler 校验层拒绝。
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
