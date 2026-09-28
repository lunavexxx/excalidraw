package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func newTestCrypto(t *testing.T, b byte) *PhoneCrypto {
	t.Helper()
	keyB64 := base64.StdEncoding.EncodeToString([]byte{b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b})
	pc, err := NewPhoneCrypto(keyB64)
	if err != nil {
		t.Fatalf("NewPhoneCrypto: %v", err)
	}
	return pc
}

func TestNewPhoneCryptoRejectsBadKey(t *testing.T) {
	if _, err := NewPhoneCrypto("not base64!!"); err == nil {
		t.Error("want error for non-base64 key")
	}
	if _, err := NewPhoneCrypto("aGVsbG8="); err == nil {
		t.Error("want error for short key")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	pc := newTestCrypto(t, 0)
	sealed, err := pc.Seal("13812345678")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if string(sealed) == "13812345678" {
		t.Error("ciphertext must differ from plaintext")
	}
	phone, err := pc.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if phone != "13812345678" {
		t.Errorf("Open = %q, want 13812345678", phone)
	}

	sealed2, err := pc.Seal("13812345678")
	if err != nil {
		t.Fatalf("Seal 2: %v", err)
	}
	if string(sealed) == string(sealed2) {
		t.Error("two seals of the same phone must differ (random nonce)")
	}
}

func TestOpenFailsAcrossMasters(t *testing.T) {
	pcA := newTestCrypto(t, 0)
	pcB := newTestCrypto(t, 0xff)
	sealed, err := pcA.Seal("13812345678")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := pcB.Open(sealed); err == nil {
		t.Error("want error opening with different master key")
	}
}

func TestHashDeterministicAndKeyBound(t *testing.T) {
	pcA := newTestCrypto(t, 0)
	if got, want := pcA.Hash("13812345678"), pcA.Hash("13812345678"); string(got) != string(want) {
		t.Error("Hash must be deterministic for lookup")
	}
	pcB := newTestCrypto(t, 0xff)
	if string(pcA.Hash("13812345678")) == string(pcB.Hash("13812345678")) {
		t.Error("Hash must differ across master keys")
	}
	if string(pcA.Hash("13812345678")) == string(pcA.Hash("13887654321")) {
		t.Error("Hash must differ across phones")
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		fails bool
	}{
		{in: "13812345678", want: "13812345678"},
		{in: "138 1234 5678", want: "13812345678"},
		{in: "+8613812345678", want: "13812345678"},
		{in: "86-138-1234-5678", want: "13812345678"},
		{in: "23812345678", fails: true},  // 非 1x 号段
		{in: "1381234567", fails: true},   // 少一位
		{in: "138123456789", fails: true}, // 多位
		{in: "12812345678", fails: true},  // 12x 号段
		{in: "abc", fails: true},
		{in: "", fails: true},
	}
	for _, tc := range cases {
		got, err := NormalizePhone(tc.in)
		if tc.fails {
			if err == nil {
				t.Errorf("NormalizePhone(%q): want error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizePhone(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	if got, want := MaskPhone("13812345678"), "138****5678"; got != want {
		t.Errorf("MaskPhone = %q, want %q", got, want)
	}
	if got := MaskPhone("123"); got != "123" {
		t.Errorf("MaskPhone(non-11) = %q, want passthrough", got)
	}
}

func TestRandomNickname(t *testing.T) {
	n := RandomNickname()
	if !strings.HasPrefix(n, "用户") || len([]rune(n)) != 9 {
		t.Errorf("RandomNickname = %q, want 用户+7位数字", n)
	}
}
