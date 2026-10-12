package commentemoji

import "testing"

func TestWholeEmojiSequences(t *testing.T) {
	for _, value := range []string{"👍", "👍🏽", "👨‍👩‍👧‍👦", "🇨🇳", "🏳️‍🌈", "❤️", "❤", "1️⃣"} {
		if _, ok := Normalize(value); !ok {
			t.Errorf("valid sequence rejected: %q", value)
		}
	}
	for _, value := range []string{"", "hello", "😀😀", "👍 text", "🇨", "🏽", "❤︎"} {
		if _, ok := Normalize(value); ok {
			t.Errorf("invalid reaction accepted: %q", value)
		}
	}
	a, _ := Normalize("❤")
	b, _ := Normalize("❤️")
	if a != b {
		t.Fatal("presentation variants must normalize")
	}
}
