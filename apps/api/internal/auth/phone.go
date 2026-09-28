package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// PhoneCrypto 对手机号做三件事:
//   - Seal/Open:AES-256-GCM 可逆密文(短信登录、客服等场景解密真号)
//   - Hash:HMAC-SHA256 确定性查找键(登录查询、唯一约束)
//   - MaskPhone:脱敏显示(138****5678),写入时算好存列,读无需解密
//
// 两组子密钥由 32 字节主密钥经 HKDF 域分离派生,主密钥来自 Nacos phone_crypto_key。
type PhoneCrypto struct {
	aesKey  []byte
	hmacKey []byte
}

func NewPhoneCrypto(masterKeyB64 string) (*PhoneCrypto, error) {
	master, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode phone_crypto_key: %w", err)
	}
	if len(master) != 32 {
		return nil, fmt.Errorf("phone_crypto_key: want 32 decoded bytes, got %d", len(master))
	}
	p := &PhoneCrypto{aesKey: make([]byte, 32), hmacKey: make([]byte, 32)}
	if err := hkdfRead(master, []byte("phone-aes"), p.aesKey); err != nil {
		return nil, err
	}
	if err := hkdfRead(master, []byte("phone-hash"), p.hmacKey); err != nil {
		return nil, err
	}
	return p, nil
}

func hkdfRead(secret, info, out []byte) error {
	r := hkdf.New(sha256.New, secret, nil, info)
	if _, err := io.ReadFull(r, out); err != nil {
		return fmt.Errorf("hkdf derive: %w", err)
	}
	return nil
}

// Seal 返回 nonce(12B) || ciphertext+tag。
func (p *PhoneCrypto) Seal(phone string) ([]byte, error) {
	gcm, err := newGCM(p.aesKey)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(phone), nil), nil
}

func (p *PhoneCrypto) Open(sealed []byte) (string, error) {
	gcm, err := newGCM(p.aesKey)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("open phone cipher: %w", err)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm, nil
}

func (p *PhoneCrypto) Hash(phone string) []byte {
	mac := hmac.New(sha256.New, p.hmacKey)
	mac.Write([]byte(phone))
	return mac.Sum(nil)
}

var phoneRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// NormalizePhone 剔除非数字字符,接受 11 位直输或 +86/86 前缀,返回 11 位号码。
func NormalizePhone(input string) (string, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, input)
	if len(digits) == 13 && strings.HasPrefix(digits, "86") {
		digits = digits[2:]
	}
	if !phoneRe.MatchString(digits) {
		return "", fmt.Errorf("invalid phone")
	}
	return digits, nil
}

func MaskPhone(phone string) string {
	if len(phone) != 11 {
		return phone
	}
	return phone[:3] + "****" + phone[7:]
}

// RandomNickname 生成 用户XXXXXXX 占位昵称(7 位随机数字)。
func RandomNickname() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000_000))
	if err != nil {
		return "用户0000000"
	}
	return "用户" + fmt.Sprintf("%07d", n.Int64())
}
