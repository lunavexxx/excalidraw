package scene

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
)

type testElement struct {
	ID           string  `json:"id"`
	Version      float64 `json:"version"`
	VersionNonce float64 `json:"versionNonce"`
	Data         string  `json:"data"`
}

func mustJSON(t testing.TB, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// contentMap 提取折叠结果的内容视图(id → 元素);折叠只承诺内容收敛,
// 数组顺序由客户端按 fractional index 修复,不在性质断言范围。
func contentMap(t testing.TB, els []json.RawMessage) map[string]testElement {
	t.Helper()
	m := map[string]testElement{}
	for _, raw := range els {
		var e testElement
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		m[e.ID] = e
	}
	return m
}

func sameContent(a, b map[string]testElement) bool {
	if len(a) != len(b) {
		return false
	}
	for id, ea := range a {
		eb, ok := b[id]
		if !ok || ea != eb {
			return false
		}
	}
	return true
}

// TestFoldRule 单点验证规则:version 高者胜;同 version 时 versionNonce 小者胜。
func TestFoldRule(t *testing.T) {
	base := []json.RawMessage{mustJSON(t, testElement{ID: "a", Version: 3, VersionNonce: 100, Data: "base"})}
	higher := []json.RawMessage{mustJSON(t, testElement{ID: "a", Version: 4, VersionNonce: 900, Data: "higher"})}
	tieLow := []json.RawMessage{mustJSON(t, testElement{ID: "a", Version: 3, VersionNonce: 50, Data: "tieLow"})}
	tieHigh := []json.RawMessage{mustJSON(t, testElement{ID: "a", Version: 3, VersionNonce: 900, Data: "tieHigh"})}

	if got := contentMap(t, FoldElements(base, higher))["a"].Data; got != "higher" {
		t.Fatalf("higher version should win, got %q", got)
	}
	if got := contentMap(t, FoldElements(base, tieLow))["a"].Data; got != "tieLow" {
		t.Fatalf("lower nonce on tie should win, got %q", got)
	}
	if got := contentMap(t, FoldElements(base, tieHigh))["a"].Data; got != "base" {
		t.Fatalf("base should win tie with lower nonce, got %q", got)
	}
}

func TestFoldMalformedSkipped(t *testing.T) {
	base := []json.RawMessage{mustJSON(t, testElement{ID: "a", Version: 1, VersionNonce: 1, Data: "a"})}
	delta := []json.RawMessage{
		json.RawMessage(`{"no_id": true}`),
		json.RawMessage(`{"id": "b"}`), // 缺 version
		json.RawMessage(`not json`),
		mustJSON(t, testElement{ID: "b", Version: 2, VersionNonce: 7, Data: "b"}),
	}
	got := contentMap(t, FoldElements(base, delta))
	if len(got) != 2 || got["a"].Data != "a" || got["b"].Data != "b" {
		t.Fatalf("malformed entries should be skipped: %+v", got)
	}
}

// TestFoldSemilattice 随机元素集上验证幂等/交换/结合(固定种子)。
func TestFoldSemilattice(t *testing.T) {
	const iterations = 500
	ids := []string{"a", "b", "c", "d", "e"}
	rng := rand.New(rand.NewSource(20260930))

	gen := func() []json.RawMessage {
		out := []json.RawMessage{}
		for _, id := range ids {
			if rng.Intn(3) == 0 {
				continue // 每批随机缺一些元素(并集语义)
			}
			out = append(out, mustJSON(t, testElement{
				ID:           id,
				Version:      float64(rng.Intn(6)),
				VersionNonce: float64(rng.Int63()),
				Data:         fmt.Sprintf("d%d", rng.Intn(1000)),
			}))
		}
		return out
	}

	for i := 0; i < iterations; i++ {
		a, b, c := gen(), gen(), gen()

		// 幂等:fold(fold(x, a), a) == fold(x, a)
		xa := FoldElements(nil, a)
		if got := FoldElements(xa, a); !sameContent(contentMap(t, got), contentMap(t, xa)) {
			t.Fatalf("iter %d: not idempotent", i)
		}

		// 交换:fold(a, b) == fold(b, a)
		ab, ba := FoldElements(a, b), FoldElements(b, a)
		if !sameContent(contentMap(t, ab), contentMap(t, ba)) {
			t.Fatalf("iter %d: not commutative", i)
		}

		// 结合:fold(fold(a,b),c) == fold(a, fold(b,c))
		abC := FoldElements(FoldElements(a, b), c)
		aBc := FoldElements(a, FoldElements(b, c))
		if !sameContent(contentMap(t, abC), contentMap(t, aBc)) {
			t.Fatalf("iter %d: not associative", i)
		}
	}
}

// TestFoldBaseOrderPreserved 新元素追加、已存在元素保持 base 顺序。
func TestFoldBaseOrderPreserved(t *testing.T) {
	base := []json.RawMessage{
		mustJSON(t, testElement{ID: "a", Version: 1, VersionNonce: 1, Data: "a"}),
		mustJSON(t, testElement{ID: "b", Version: 1, VersionNonce: 2, Data: "b"}),
	}
	delta := []json.RawMessage{
		mustJSON(t, testElement{ID: "c", Version: 1, VersionNonce: 3, Data: "c"}),
		mustJSON(t, testElement{ID: "a", Version: 2, VersionNonce: 9, Data: "a2"}),
	}
	got := FoldElements(base, delta)
	if len(got) != 3 {
		t.Fatalf("want 3 elements, got %d", len(got))
	}
	var ids []string
	for _, raw := range got {
		var e testElement
		_ = json.Unmarshal(raw, &e)
		ids = append(ids, e.ID)
	}
	want := []string{"a", "b", "c"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order = %v, want %v", ids, want)
		}
	}
	if contentMap(t, got)["a"].Data != "a2" {
		t.Fatal("updated element content should win")
	}
}

func TestSplitAndBuildSceneData(t *testing.T) {
	appState := mustJSON(t, map[string]any{"viewBackgroundColor": "#ffffff"})
	data := mustJSON(t, map[string]any{
		"elements": []any{
			map[string]any{"id": "a", "version": 1, "versionNonce": 1},
		},
		"appState": json.RawMessage(appState),
	})
	els, as, err := SplitSceneData(data)
	if err != nil || len(els) != 1 || string(as) != string(appState) {
		t.Fatalf("split = %v, %s, %v", els, as, err)
	}
	rebuilt, err := BuildSceneData(els, as)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	els2, as2, err := SplitSceneData(rebuilt)
	if err != nil || len(els2) != 1 || string(as2) != string(appState) {
		t.Fatalf("roundtrip = %v, %s, %v", els2, as2, err)
	}

	// 空数据 → 空拆分。
	if els, as, err := SplitSceneData(nil); err != nil || els != nil || as != nil {
		t.Fatalf("empty split = %v, %v, %v", els, as, err)
	}
	// elements 缺失 → error。
	if _, _, err := SplitSceneData(mustJSON(t, map[string]any{"foo": 1})); err != nil {
		t.Fatalf("missing elements should not error (treated as empty): %v", err)
	}
	// elements 非数组 → error。
	if _, _, err := SplitSceneData(mustJSON(t, map[string]any{"elements": 5})); err == nil {
		t.Fatal("non-array elements should error")
	}
}
