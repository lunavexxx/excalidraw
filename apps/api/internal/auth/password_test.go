package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("p@ssw0rd-中文")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "p@ssw0rd-中文" {
		t.Error("hash must not equal plaintext")
	}
	if !VerifyPassword(hash, "p@ssw0rd-中文") {
		t.Error("VerifyPassword: want true for correct password")
	}
	if VerifyPassword(hash, "wrong") {
		t.Error("VerifyPassword: want false for wrong password")
	}
}
