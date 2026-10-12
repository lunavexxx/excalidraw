// Package commentemoji validates whole emoji sequences using pinned Unicode data.
package commentemoji

import (
	"embed"
	"strings"
)

//go:embed sequences.txt
var data embed.FS

var sequences = func() map[string]string {
	raw, err := data.ReadFile("sequences.txt")
	if err != nil {
		panic(err)
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		emoji, _, _ := strings.Cut(line, "\t")
		result[strings.ReplaceAll(emoji, "\ufe0f", "")] = emoji
	}
	return result
}()

// Normalize accepts qualified and unqualified forms of one RGI emoji, preserving
// skin tones, flags and ZWJ sequences. Adjacent independent emoji are rejected.
func Normalize(value string) (string, bool) {
	if len(value) > 128 {
		return "", false
	}
	emoji, ok := sequences[strings.ReplaceAll(value, "\ufe0f", "")]
	return emoji, ok
}
