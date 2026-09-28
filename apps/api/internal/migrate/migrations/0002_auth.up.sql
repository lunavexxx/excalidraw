-- M1 账号体系:用户 + 刷新令牌(plans/collab-platform.md)。
-- 手机号不落明文:phone_cipher 为 AES-256-GCM 可逆密文(短信登录/客服场景解密),
-- phone_hash 为 HMAC-SHA256 确定性查找键(唯一约束),phone_masked 为脱敏显示值;
-- 手机号格式校验在应用层(密文列无法加 CHECK)。密码 bcrypt 不可逆。

CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  phone_cipher  BYTEA NOT NULL,
  phone_hash    BYTEA NOT NULL,
  phone_masked  TEXT NOT NULL,
  country_code  TEXT NOT NULL DEFAULT '+86',
  password_hash TEXT NOT NULL,
  nickname      TEXT NOT NULL CHECK (char_length(nickname) BETWEEN 1 AND 20),
  avatar_url    TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT users_phone_hash_unique UNIQUE (phone_hash)
);

CREATE TABLE refresh_tokens (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash BYTEA NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens(user_id);
