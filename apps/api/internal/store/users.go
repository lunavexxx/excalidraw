package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrPhoneTaken 表示注册手机号已存在(phone_hash 唯一冲突)。
var ErrPhoneTaken = errors.New("store: phone already registered")

// ErrUserNotFound 用户不存在(手机号未注册 / ID 无效)。
var ErrUserNotFound = errors.New("store: user not found")

// User 是 users 行的应用层视图。PasswordHash 仅服务端登录校验使用,
// 永不出现在 API 响应里(响应字段由 handler 手工构造)。
type User struct {
	ID           string
	PhoneMasked  string
	CountryCode  string
	PasswordHash []byte
	Nickname     string
	AvatarURL    string
	CreatedAt    time.Time
}

type NewUser struct {
	PhoneCipher  []byte
	PhoneHash    []byte
	PhoneMasked  string
	CountryCode  string
	PasswordHash []byte
	Nickname     string
}

func (db *DB) CreateUser(ctx context.Context, u NewUser) (User, error) {
	row := db.pool.QueryRow(ctx, `
		INSERT INTO users (phone_cipher, phone_hash, phone_masked, country_code, password_hash, nickname)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, phone_masked, country_code, nickname, password_hash, COALESCE(avatar_url, ''), created_at`,
		u.PhoneCipher, u.PhoneHash, u.PhoneMasked, u.CountryCode, u.PasswordHash, u.Nickname)
	return scanUser(row)
}

func (db *DB) GetUserByPhoneHash(ctx context.Context, phoneHash []byte) (User, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT id, phone_masked, country_code, nickname, password_hash, COALESCE(avatar_url, ''), created_at
		FROM users WHERE phone_hash = $1`, phoneHash)
	return scanUser(row)
}

func (db *DB) GetUserByID(ctx context.Context, id string) (User, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT id, phone_masked, country_code, nickname, password_hash, COALESCE(avatar_url, ''), created_at
		FROM users WHERE id = $1`, id)
	return scanUser(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.PhoneMasked, &u.CountryCode, &u.Nickname, &u.PasswordHash, &u.AvatarURL, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return User{}, ErrPhoneTaken
		}
		return User{}, err
	}
	return u, nil
}
