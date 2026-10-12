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
	// Establish the receipt boundary before accepting connections.
	consumer, err := h.newOutboxConsumer(ctx)
	if err != nil {
		log.Printf("[room] initial outbox boundary unavailable: %v", err)
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		patrol := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if consumer == "" {
					var err error
					consumer, err = h.newOutboxConsumer(ctx)
					if err != nil {
						log.Printf("[room] access outbox unavailable: %v", err)
						continue
					}
				}
				_, err := h.persist.pool.Exec(ctx, `INSERT INTO collaboration_consumers(id) VALUES($1) ON CONFLICT(id) DO UPDATE SET heartbeat_at=now()`, consumer)
				if err != nil {
					continue
				}
				rows, err := h.persist.pool.Query(ctx, `SELECT o.id,COALESCE(o.canvas_id::text,''),COALESCE(o.recipient_id::text,''),o.type,COALESCE(o.thread_id::text,''),COALESCE(o.source_version,0) FROM collaboration_outbox o WHERE NOT EXISTS(SELECT 1 FROM collaboration_outbox_receipts r WHERE r.consumer_id=$1 AND r.event_id=o.id) ORDER BY o.id LIMIT 500`, consumer)
				if err == nil {
					type event struct {
						id               int64
						canvasID, userID string
						kind, threadID   string
						version          int64
					}
					events := []event{}
					for rows.Next() {
						var e event
						if rows.Scan(&e.id, &e.canvasID, &e.userID, &e.kind, &e.threadID, &e.version) == nil {
							events = append(events, e)
						}
					}
					rowErr := rows.Err()
					rows.Close()
					if rowErr == nil {
						acknowledged := []int64{}
						for _, e := range events {
							for _, s := range h.localSockets() {
								session, ok := s.Data().(*Session)
								if !ok {
									continue
								}
								if e.userID != "" && session.UserID == e.userID {
									if e.kind == "comment_reads" {
										s.Emit("comment-reads-changed", map[string]any{"canvas_id": e.canvasID, "thread_id": e.threadID, "unread_version": e.version})
									} else {
										s.Emit("notifications-changed")
									}
								}
								if e.canvasID != "" && e.kind != "comment_reads" && s.Rooms().Has(socket.Room(e.canvasID)) {
									if e.kind == "comments" {
										if h.checkRoomAccess(s, session, e.canvasID, false) {
											s.Emit("comments-changed", map[string]any{"canvas_id": e.canvasID, "thread_id": e.threadID, "version": e.version})
										}
									} else {
										h.checkRoomAccess(s, session, e.canvasID, true)
										s.Emit("canvas-members-changed", e.canvasID)
									}
								}
							}
							acknowledged = append(acknowledged, e.id)
						}
						if len(acknowledged) > 0 {
							_, err = h.persist.pool.Exec(ctx, `INSERT INTO collaboration_outbox_receipts(consumer_id,event_id) SELECT $1,unnest($2::bigint[]) ON CONFLICT DO NOTHING`, consumer, acknowledged)
							if err != nil {
								log.Printf("[room] outbox acknowledgement failed: %v", err)
							}
						}
					}
				}
				patrol++
				if patrol >= 20 {
					patrol = 0
					_, _ = h.persist.pool.Exec(ctx, `DELETE FROM collaboration_consumers WHERE heartbeat_at<now()-interval '5 minutes'`)
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

// A new instance skips only rows visible in this transaction. A lower ID that
// commits later remains unacknowledged and is delivered by the next poll.
func (h *RoomHub) newOutboxConsumer(ctx context.Context) (string, error) {
	tx, err := h.persist.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO collaboration_consumers DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO collaboration_outbox_receipts(consumer_id,event_id) SELECT $1,id FROM collaboration_outbox`, id); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}
