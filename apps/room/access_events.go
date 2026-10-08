package main

import (
	"context"
	"encoding/json"
	"github.com/zishang520/socket.io/servers/socket/v3"
	"log"
	"time"
)

type Presence struct {
	SocketID  string `json:"socket_id"`
	UserID    string `json:"user_id"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	Role      string `json:"role"`
}

func (h *RoomHub) emitPresence(roomID string, sockets []*socket.RemoteSocket, excluded string) {
	people := []map[string]string{}
	for _, s := range sockets {
		if string(s.Id()) == excluded {
			continue
		}
		raw, err := json.Marshal(s.Data())
		if err != nil {
			continue
		}
		var data struct {
			UserID    string            `json:"user_id"`
			Nickname  string            `json:"nickname"`
			AvatarURL string            `json:"avatar_url"`
			Roles     map[string]string `json:"roles"`
		}
		if json.Unmarshal(raw, &data) != nil || data.UserID == "" {
			continue
		}
		people = append(people, map[string]string{"socket_id": string(s.Id()), "user_id": data.UserID, "nickname": data.Nickname, "avatar_url": data.AvatarURL, "role": data.Roles[roomID]})
	}
	h.io.To(socket.Room(roomID)).Emit("presence-roster", people)
}
func (h *RoomHub) broadcastPresence(roomID, excluded string) {
	if !IsCanvasRoom(roomID) {
		return
	}
	h.io.In(socket.Room(roomID)).FetchSockets()(func(sockets []*socket.RemoteSocket, err error) {
		if err == nil {
			h.emitPresence(roomID, sockets, excluded)
		}
	})
}
func (h *RoomHub) checkRoomAccess(s *socket.Socket, session *Session, roomID string, force bool) bool {
	session.accessMu.Lock()
	defer session.accessMu.Unlock()
	if session.TokenExp > 0 && session.TokenExp <= time.Now().Unix() {
		s.Disconnect(true)
		return false
	}
	if force {
		session.invalidateACL(roomID)
	}
	role, err := h.acl.ResolveAccess(context.Background(), session, roomID)
	previous := session.roleOf(roomID)
	if err != nil {
		session.setRole(roomID, "")
		s.Emit("canvas-access-changed", map[string]any{"canvas_id": roomID, "role": previous, "unavailable": true})
		return false
	}
	if role == "" {
		session.setRole(roomID, "")
		s.Emit("canvas-access-changed", map[string]string{"canvas_id": roomID, "role": ""})
		s.Leave(socket.Room(roomID))
		h.broadcastPresence(roomID, "")
		return false
	}
	session.setRole(roomID, role)
	if role != previous || force {
		s.Emit("canvas-access-changed", map[string]string{"canvas_id": roomID, "role": role})
		h.broadcastPresence(roomID, "")
	}
	return true
}
func (h *RoomHub) localSockets() []*socket.Socket {
	h.localMu.Lock()
	defer h.localMu.Unlock()
	items := make([]*socket.Socket, 0, len(h.local))
	for _, s := range h.local {
		items = append(items, s)
	}
	return items
}

// Each instance consumes the durable outbox independently. A missed hint is
// recovered by a periodic ACL check and API notification reads after reconnect.
func (h *RoomHub) StartAccessEvents(ctx context.Context) {
	go func() {
		var cursor int64
		if err := h.persist.pool.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM collaboration_outbox`).Scan(&cursor); err != nil {
			log.Printf("[room] access outbox unavailable: %v", err)
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		patrol := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := h.persist.pool.Query(ctx, `SELECT id,COALESCE(canvas_id::text,''),COALESCE(recipient_id::text,'') FROM collaboration_outbox WHERE id>$1 ORDER BY id LIMIT 500`, cursor)
				if err == nil {
					type event struct {
						id               int64
						canvasID, userID string
					}
					events := []event{}
					for rows.Next() {
						var e event
						if rows.Scan(&e.id, &e.canvasID, &e.userID) == nil {
							events = append(events, e)
						}
					}
					rowErr := rows.Err()
					rows.Close()
					if rowErr == nil {
						for _, e := range events {
							for _, s := range h.localSockets() {
								session, ok := s.Data().(*Session)
								if !ok {
									continue
								}
								if e.userID != "" && session.UserID == e.userID {
									s.Emit("notifications-changed")
								}
								if e.canvasID != "" && s.Rooms().Has(socket.Room(e.canvasID)) {
									h.checkRoomAccess(s, session, e.canvasID, true)
									s.Emit("canvas-members-changed", e.canvasID)
								}
							}
							cursor = e.id
						}
					}
				}
				patrol++
				if patrol >= 20 {
					patrol = 0
					for _, s := range h.localSockets() {
						session, ok := s.Data().(*Session)
						if !ok {
							continue
						}
						if session.TokenExp > 0 && session.TokenExp <= time.Now().Unix() {
							s.Disconnect(true)
							continue
						}
						for room := range s.Rooms().All() {
							if IsCanvasRoom(string(room)) {
								h.checkRoomAccess(s, session, string(room), true)
							}
						}
					}
				}
			}
		}
	}()
}
