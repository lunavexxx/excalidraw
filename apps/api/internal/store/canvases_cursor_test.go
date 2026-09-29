package store

import (
	"testing"
	"time"
)

func TestCanvasCursorRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Nanosecond)
	id := "01987654-3210-fedc-ba98-76543210fedc"
	encoded := EncodeCanvasCursor(now, id)

	gotT, gotID, err := DecodeCanvasCursor(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotT.Equal(now) {
		t.Fatalf("time mismatch: want %v got %v", now, gotT)
	}
	if gotID != id {
		t.Fatalf("id mismatch: want %q got %q", id, gotID)
	}
}

func TestDecodeCanvasCursorRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"not base64!!", "aGVsbG8", ""} {
		if bad == "" {
			continue
		}
		if _, _, err := DecodeCanvasCursor(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
