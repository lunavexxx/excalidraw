package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRefreshTokenInvalid 表示刷新令牌不存在、已撤销或已过期(三者统一,
// 不向调用方区分,避免泄露令牌状态)。
var ErrRefreshTokenInvalid = errors.New("store: refresh token invalid")

// RotateRefreshToken 原子地把 tokenHash 对应的活跃令牌置为已撤销并返回其归属;
// 令牌不存在/已撤销/已过期时返回 ErrRefreshTokenInvalid。单条 UPDATE 完成
// 检查+撤销,避免并发刷新复用同一令牌。
func (db *DB) RotateRefreshToken(ctx context.Context, tokenHash []byte, newHash []byte, newExpiresAt time.Time) (userID string, err error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	var id int64
	err = tx.QueryRow(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING id, user_id`, tokenHash).Scan(&id, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRefreshTokenInvalid
	}
	if err != nil {
		return "", err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`, userID, newHash, newExpiresAt)
	if err != nil {
		return "", err
	}
	return userID, tx.Commit(ctx)
}

func (db *DB) CreateRefreshToken(ctx context.Context, userID string, tokenHash []byte, expiresAt time.Time) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`, userID, tokenHash, expiresAt)
	return err
}

func (db *DB) RevokeRefreshToken(ctx context.Context, tokenHash []byte) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}
