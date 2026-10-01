package main

import (
	"encoding/json"
	"testing"

	"github.com/zishang520/socket.io/v3/pkg/types"
)

func TestParseSceneUpdate(t *testing.T) {
	raw := []byte(`{"type":"SCENE_UPDATE","payload":{"elements":[{"id":"e1"},{"id":"e2"}]}}`)
	elements, frame, ok := parseSceneUpdate(raw)
	if !ok {
		t.Fatal("valid frame rejected")
	}
	// elements 必须原样透传(字节级,进 ::jsonb 前不重编组)。
	if string(elements) != `[{"id":"e1"},{"id":"e2"}]` {
		t.Errorf("elements = %s", elements)
	}
	if frame["type"] != "SCENE_UPDATE" {
		t.Errorf("frame = %v", frame)
	}
	// 转发帧对象展开后可补 seq 再编组(全量键保留)。
	frame["seq"] = int64(9)
	out, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back["seq"].(float64) != 9 {
		t.Errorf("seq = %v", back["seq"])
	}
}

func TestParseSceneUpdateRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"wrong type", `{"type":"MOUSE_LOCATION","payload":{"elements":[1]}}`},
		{"missing payload", `{"type":"SCENE_UPDATE"}`},
		{"elements not array", `{"type":"SCENE_UPDATE","payload":{"elements":{"a":1}}}`},
		{"empty elements", `{"type":"SCENE_UPDATE","payload":{"elements":[]}}`},
		{"garbage", `{{{`},
		{"empty", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := parseSceneUpdate([]byte(tc.raw)); ok {
				t.Errorf("must reject: %s", tc.raw)
			}
		})
	}
}

func TestFrameBytes(t *testing.T) {
	// JS Uint8Array 送达形态:BufferInterface(spike 实测 *types.BytesBuffer)。
	buf := types.NewBytesBuffer([]byte(`{"type":"SCENE_UPDATE"}`))
	if got := frameBytes(buf); string(got) != `{"type":"SCENE_UPDATE"}` {
		t.Errorf("buffer form: %s", got)
	}
	if got := frameBytes([]byte(`raw`)); string(got) != `raw` {
		t.Errorf("bytes form: %s", got)
	}
	if got := frameBytes(`"str"`); string(got) != `"str"` {
		t.Errorf("string form: %s", got)
	}
	if got := frameBytes(map[string]any{"a": float64(1)}); string(got) != `{"a":1}` {
		t.Errorf("map form: %s", got)
	}
	if frameBytes(3.14) != nil {
		t.Error("unsupported form must return nil")
	}
}

func TestIsCanvasRoom(t *testing.T) {
	if !IsCanvasRoom(testCanvas) {
		t.Error("uuid accepted")
	}
	for _, bad := range []string{"", "follow@abc", "0B3F2A1C-1111-4222-8333-44445555666G", "short"} {
		if IsCanvasRoom(bad) {
			t.Errorf("must reject %q", bad)
		}
	}
}
