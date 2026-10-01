// Package scene 实现服务端折叠的纯规则(实现规约见 M3 方案 §一)。
//
// 合并单元是带版本戳的元素状态快照(非操作日志)。规则与客户端
// packages/excalidraw/data/reconcile.ts 的对称全序部分一致:
//
//	按元素 id 分组;version 高者胜;同 version 时 versionNonce 小者胜。
//
// 该规则满足幂等/交换/结合(每元素状态构成 join-semilattice),因此
// 事件可乱序到达、重复投递、多实例并发折叠,结果唯一。客户端的
// bumpElementVersions 与"编辑中偏置"是刻意打破交换律的交互保护,
// 绝不在此复制。store 与 canvas 包共用,本包不得依赖内部其他包。
package scene

import (
	"encoding/json"
)

type foldEntry struct {
	raw     json.RawMessage
	id      string
	version float64
	nonce   float64
}

// FoldElements 按元素 id 折叠多批元素。
// base 保序,新元素按首次出现顺序追加在尾部;缺 id 或类型不符的项跳过
// (客户端 restoreElements 会做最终校验)。z 序(fractional index)是元素
// 状态的一部分,随元素参与同一 LWW;排序修复由客户端 syncInvalidIndices 完成。
func FoldElements(base []json.RawMessage, deltas ...[]json.RawMessage) []json.RawMessage {
	index := make(map[string]int, len(base))
	out := make([]json.RawMessage, 0, len(base))
	winner := make([]foldEntry, 0, len(base)+64)

	add := func(e foldEntry) {
		if i, ok := index[e.id]; ok {
			w := winner[i]
			if e.version > w.version || (e.version == w.version && e.nonce < w.nonce) {
				winner[i] = e
				out[i] = e.raw
			}
			return
		}
		index[e.id] = len(winner)
		winner = append(winner, e)
		out = append(out, e.raw)
	}

	for _, raw := range base {
		if e, ok := parseElement(raw); ok {
			add(e)
		}
	}
	for _, delta := range deltas {
		for _, raw := range delta {
			if e, ok := parseElement(raw); ok {
				add(e)
			}
		}
	}
	return out
}

// parseElement 从序列化元素中提取折叠所需的最小视图。
func parseElement(raw json.RawMessage) (foldEntry, bool) {
	var probe struct {
		ID           *string  `json:"id"`
		Version      *float64 `json:"version"`
		VersionNonce *float64 `json:"versionNonce"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return foldEntry{}, false
	}
	if probe.ID == nil || *probe.ID == "" || probe.Version == nil {
		return foldEntry{}, false
	}
	nonce := 0.0
	if probe.VersionNonce != nil {
		nonce = *probe.VersionNonce
	}
	return foldEntry{raw: raw, id: *probe.ID, version: *probe.Version, nonce: nonce}, true
}

// SplitSceneData 把 M2 场景 JSONB({elements, appState})拆开;
// elements 缺失或不是数组时返回 error(appState 可缺省)。
func SplitSceneData(data []byte) (elements []json.RawMessage, appState json.RawMessage, err error) {
	if len(data) == 0 {
		return nil, nil, nil
	}
	var probe struct {
		Elements *[]json.RawMessage `json:"elements"`
		AppState json.RawMessage    `json:"appState"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, err
	}
	if probe.Elements != nil {
		elements = *probe.Elements
	}
	return elements, probe.AppState, nil
}

// BuildSceneData 重组场景 JSONB。
func BuildSceneData(elements []json.RawMessage, appState json.RawMessage) ([]byte, error) {
	if elements == nil {
		elements = []json.RawMessage{}
	}
	payload := struct {
		Elements []json.RawMessage `json:"elements"`
		AppState json.RawMessage   `json:"appState,omitempty"`
	}{Elements: elements, AppState: appState}
	return json.Marshal(payload)
}
