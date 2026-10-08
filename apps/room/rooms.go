// 协议核心(上游 excalidraw-room 事件语义 + 本项目增改)。
// 上游事件全集:emit join-room/server-broadcast/server-volatile-broadcast/user-follow;
// on init-room/new-user/room-user-change/client-broadcast/first-in-room/user-follow-room-change。
// 增改:join-room 带 ack 回传角色;拒绝时 emit join-room-error(fail-closed);
// viewer 的 server-broadcast 被服务端丢弃(共享编辑仅及内容的强制点);
// 协议 v2:场景消息先落库后转发(帧携带 seq),新增 sync-request 状态补丁。
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"github.com/zishang520/socket.io/servers/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

const followRoomPrefix = "follow@"

// debugRoster 由 ROOM_DEBUG_ROSTER 开启:打印 join 时的跨实例聚合名册。
var debugRoster = os.Getenv("ROOM_DEBUG_ROSTER") != ""

type RoomHub struct {
	io      *socket.Server
	cfg     *Config
	persist *Persister
	sync    *SceneSync
	acl     *ACLResolver
	rate    *RateLimiter
	conns   *ConnectionGuard
	localMu sync.Mutex
	local   map[string]*socket.Socket
}

func NewRoomHub(io *socket.Server, cfg *Config, persist *Persister, sync *SceneSync, acl *ACLResolver, rate *RateLimiter, conns *ConnectionGuard) *RoomHub {
	return &RoomHub{io: io, cfg: cfg, persist: persist, sync: sync, acl: acl, rate: rate, conns: conns, local: make(map[string]*socket.Socket)}
}

func (h *RoomHub) Register() {
	h.io.On("connection", func(args ...any) {
		s, ok := args[0].(*socket.Socket)
		if !ok {
			return
		}
		session, ok := s.Data().(*Session)
		if !ok || session == nil {
			// 握手鉴权 fail-closed,正常不可达;防御性断开。
			s.Disconnect(true)
			return
		}
		// 连接数按握手身份计(access userId / guest subject):IP 在反代/NAT
		// 后不唯一,不可作限制键。
		identity := session.Identity()
		if session.UserID != "" && h.persist != nil && h.persist.pool != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = h.persist.pool.QueryRow(ctx, `SELECT nickname,COALESCE(avatar_url,'') FROM users WHERE id=$1`, session.UserID).Scan(&session.DisplayName, &session.AvatarURL)
			cancel()
		} else {
			session.DisplayName = "Guest"
		}
		if !h.conns.Acquire(identity) {
			s.Disconnect(true)
			return
		}
		h.localMu.Lock()
		h.local[string(s.Id())] = s
		h.localMu.Unlock()
		s.On("disconnect", func(_ ...any) {
			h.conns.Release(identity)
			h.rate.Release(string(s.Id()))
			h.localMu.Lock()
			delete(h.local, string(s.Id()))
			h.localMu.Unlock()
		})

		s.Emit("init-room")
		s.On("presence-request", func(evArgs ...any) {
			if len(evArgs) > 0 {
				if roomID, ok := evArgs[0].(string); ok && s.Rooms().Has(socket.Room(roomID)) {
					h.broadcastPresence(roomID, "")
				}
			}
		})

		s.On("join-room", func(evArgs ...any) {
			h.onJoinRoom(s, session, evArgs)
		})

		s.On("server-broadcast", func(evArgs ...any) {
			h.onServerBroadcast(s, session, evArgs)
		})

		s.On("sync-request", func(evArgs ...any) {
			h.onSyncRequest(s, evArgs)
		})

		s.On("server-volatile-broadcast", func(evArgs ...any) {
			h.onServerVolatileBroadcast(s, evArgs)
		})

		s.On("user-follow", func(evArgs ...any) {
			h.onUserFollow(s, evArgs)
		})

		s.On("disconnecting", func(_ ...any) {
			for room := range s.Rooms().All() {
				if string(room) == string(s.Id()) {
					continue
				}
				h.io.In(room).FetchSockets()(func(sockets []*socket.RemoteSocket, err error) {
					if err != nil {
						return
					}
					// 将离者此刻仍在房间(fetchSockets 含自身),名册须剔除,
					// 否则对端收到幽灵成员且无人纠正(Node 版既有 bug,此处修正)。
					h.io.To(room).Emit("room-user-change", rosterIDs(sockets, string(s.Id())))
					h.emitPresence(string(room), sockets, string(s.Id()))
				})
			}
		})
	})
}

func (h *RoomHub) onJoinRoom(s *socket.Socket, session *Session, evArgs []any) {
	ack, hasAck := lastAck(evArgs)
	if len(evArgs) == 0 {
		return
	}
	roomID, _ := evArgs[0].(string)
	if !IsCanvasRoom(roomID) {
		deny(s, ack, hasAck, "invalid_room")
		return
	}
	if !h.rate.Allow(string(s.Id()), time.Now().UnixMilli()) {
		deny(s, ack, hasAck, "rate_limited")
		return
	}
	role, err := h.acl.ResolveRole(context.Background(), session, roomID)
	if err != nil || role == "" {
		deny(s, ack, hasAck, "forbidden")
		return
	}
	session.setRole(roomID, role)
	s.Join(socket.Room(roomID))

	// 判空与名册均经 redis adapter 聚合(多实例语义)。
	h.io.In(socket.Room(roomID)).FetchSockets()(func(sockets []*socket.RemoteSocket, err error) {
		if err != nil {
			log.Printf("[room] fetchSockets %s: %v", roomID, err)
			return
		}
		if debugRoster {
			ids := make([]string, 0, len(sockets))
			for _, rs := range sockets {
				ids = append(ids, string(rs.Id()))
			}
			log.Printf("[room] join %s node=%d socket=%s aggregated=%v", roomID, h.cfg.Port, s.Id(), ids)
		}
		if len(sockets) <= 1 {
			s.Emit("first-in-room")
		} else {
			s.To(socket.Room(roomID)).Emit("new-user", s.Id())
		}
		h.io.To(socket.Room(roomID)).Emit("room-user-change", rosterIDs(sockets, ""))
		h.emitPresence(roomID, sockets, "")
		if hasAck {
			ack([]any{map[string]any{"role": role}}, nil)
		}
	})
}

func (h *RoomHub) onServerBroadcast(s *socket.Socket, session *Session, evArgs []any) {
	ack, hasAck := lastAck(evArgs)
	if len(evArgs) < 2 {
		return
	}
	roomID, _ := evArgs[0].(string)
	if roomID == "" {
		return
	}
	// 只转发/落库自己已通过 ACL 加入的画布房间(viewer 一律丢弃:
	// 场景更新只走本事件,presence 走 volatile,分叉即强制点)。
	if !s.Rooms().Has(socket.Room(roomID)) || !h.checkRoomAccess(s, session, roomID, false) || session.roleOf(roomID) == "viewer" {
		return
	}
	if !h.rate.Allow(string(s.Id()), time.Now().UnixMilli()) {
		return
	}
	raw := frameBytes(evArgs[1])
	if raw == nil {
		return
	}
	// 协议 v2:解码二进制 JSON 帧 → 只认 SCENE_UPDATE 增量 →
	// persist-then-relay(帧携带落库 seq,先落库后转发);
	// 落库失败不转发,客户端巡检 + scene-diff 自愈。
	elements, frame, ok := parseSceneUpdate(raw)
	if !ok {
		return
	}
	h.persist.Append(roomID, elements, func(seq int64) {
		frame["seq"] = seq
		out, err := json.Marshal(frame)
		if err != nil {
			return
		}
		s.To(socket.Room(roomID)).Emit("client-broadcast", out)
		if hasAck {
			ack([]any{map[string]any{"seq": seq}}, nil)
		}
	}, func(err error) {
		if hasAck {
			ack([]any{map[string]any{"error": "persist_failed"}}, nil)
		}
	})
}

// parseSceneUpdate 校验线上帧并拆出落库与转发两份数据:
// 返回 (elements 原始字节(原样进 ::jsonb), 展开后的转发帧对象, 是否合法)。
// 只认 type=SCENE_UPDATE 且 payload.elements 为非空数组的帧。
func parseSceneUpdate(raw []byte) ([]byte, map[string]any, bool) {
	var probe struct {
		Type    string `json:"type"`
		Payload *struct {
			Elements json.RawMessage `json:"elements"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Type != "SCENE_UPDATE" || probe.Payload == nil {
		return nil, nil, false
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(probe.Payload.Elements, &elements); err != nil || len(elements) == 0 {
		return nil, nil, false
	}
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, nil, false
	}
	return probe.Payload.Elements, frame, true
}

func (h *RoomHub) onSyncRequest(s *socket.Socket, evArgs []any) {
	ack, hasAck := lastAck(evArgs)
	if len(evArgs) == 0 {
		return
	}
	roomID, _ := evArgs[0].(string)
	// ack 缺失无法回执(客户端 .timeout(5000) 自行超时);roomId 须为已入房。
	if !hasAck || roomID == "" {
		return
	}
	session, _ := s.Data().(*Session)
	if session == nil || !s.Rooms().Has(socket.Room(roomID)) || !h.checkRoomAccess(s, session, roomID, false) {
		ack([]any{map[string]any{"error": "forbidden"}}, nil)
		return
	}
	afterSeq := int64(0)
	if len(evArgs) > 1 {
		afterSeq = ParseAfterSeq(evArgs[1])
	}
	// 任意已入房角色可用(viewer 也可,只读路径)。
	patch, err := h.sync.Fetch(context.Background(), roomID, afterSeq)
	if err != nil {
		ack([]any{map[string]any{"error": "sync_failed"}}, nil)
		return
	}
	ack([]any{patch}, nil)
}

func (h *RoomHub) onServerVolatileBroadcast(s *socket.Socket, evArgs []any) {
	if len(evArgs) < 2 {
		return
	}
	roomID, _ := evArgs[0].(string)
	if roomID == "" || len(evArgs) < 2 {
		return
	}
	// 允许:已加入的房间,或自己专属的 follow@<socketId> 动态房间
	// (滚动跟随:被跟随者不在该房间内,由 followers 加入)。
	followRoom := followRoomPrefix + string(s.Id())
	if !s.Rooms().Has(socket.Room(roomID)) && roomID != followRoom {
		return
	}
	if !h.rate.Allow(string(s.Id()), time.Now().UnixMilli()) {
		return
	}
	s.Volatile().To(socket.Room(roomID)).Emit("client-broadcast", evArgs[1])
}

func (h *RoomHub) onUserFollow(s *socket.Socket, evArgs []any) {
	if len(evArgs) == 0 {
		return
	}
	payload, _ := evArgs[0].(map[string]any)
	if payload == nil {
		return
	}
	target, _ := payload["userToFollow"].(map[string]any)
	if target == nil {
		return
	}
	targetID, _ := target["socketId"].(string)
	action, _ := payload["action"].(string)
	if targetID == "" || (action != "FOLLOW" && action != "UNFOLLOW") {
		return
	}
	room := socket.Room(followRoomPrefix + targetID)
	if action == "FOLLOW" {
		s.Join(room)
	} else {
		s.Leave(room)
	}
	h.io.In(room).FetchSockets()(func(sockets []*socket.RemoteSocket, err error) {
		if err != nil {
			return
		}
		h.io.To(socket.Room(socket.SocketId(targetID))).Emit("user-follow-room-change", rosterIDs(sockets, ""))
	})
}

func deny(s *socket.Socket, ack socket.Ack, hasAck bool, reason string) {
	s.Emit("join-room-error", map[string]any{"reason": reason})
	if hasAck {
		ack([]any{map[string]any{"error": reason}}, nil)
	}
}

// lastAck 取末位 ack 参数(socket.io 仅在客户端要求 ack 时追加)。
func lastAck(args []any) (socket.Ack, bool) {
	if len(args) == 0 {
		return nil, false
	}
	if ack, ok := args[len(args)-1].(socket.Ack); ok {
		return ack, true
	}
	return nil, false
}

// rosterIDs 汇总房间成员 socketId;exclude 非空时剔除(断开重播用)。
func rosterIDs(sockets []*socket.RemoteSocket, exclude string) []string {
	ids := make([]string, 0, len(sockets))
	for _, rs := range sockets {
		if id := string(rs.Id()); id != exclude {
			ids = append(ids, id)
		}
	}
	return ids
}

// frameBytes 提取线上帧的原始 JSON 字节(二进制/字符串/对象形态均收)。
func frameBytes(v any) []byte {
	switch data := v.(type) {
	case types.BufferInterface:
		return data.Bytes()
	case []byte:
		return data
	case string:
		return []byte(data)
	case map[string]any:
		out, err := json.Marshal(data)
		if err != nil {
			return nil
		}
		return out
	default:
		return nil
	}
}
