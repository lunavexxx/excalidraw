package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"github.com/lunavexxx/excalidraw/apps/api/internal/testdb"
)

func TestCommentAnchorRebindingAndReplyPagination(t *testing.T) {
	ctx := context.Background()
	url := testdb.URL(t)
	db, err := store.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	u, err := db.CreateUser(ctx, store.NewUser{PhoneCipher: []byte("pin"), PhoneHash: []byte("pin"), PhoneMasked: "138****0000", CountryCode: "+86", PasswordHash: []byte("hash"), Nickname: "pins"})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := db.CreateCanvas(ctx, u.ID, "pin rebinding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO canvas_scenes(canvas_id,version,data) VALUES($1,1,'{"elements":[{"id":"old","x":50,"y":60,"width":100,"height":100,"version":10,"versionNonce":4},{"id":"new","x":100,"y":200,"width":40,"height":40,"version":1,"versionNonce":2}]}') ON CONFLICT(canvas_id) DO UPDATE SET data=EXCLUDED.data`, cv.ID); err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateComment(ctx, cv.ID, "", u.ID, store.CommentInput{Body: "pin", RequestID: "88888888-8888-4888-8888-888888888888", Anchor: store.CommentAnchor{X: 0, Y: 0, ElementID: "old", U: .5, V: .5}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := db.GetCommentThread(ctx, cv.ID, id, u.ID, true, 0)
	if err != nil || detail.Thread.Anchor.X != 100 || detail.Thread.Anchor.Y != 110 {
		t.Fatal("seed from current scene", detail.Thread.Anchor, err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO canvas_events(canvas_id,elements) VALUES($1,'[{"id":"old","x":0,"y":0,"width":20,"height":20,"version":2,"versionNonce":1}]')`, cv.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateCommentThread(ctx, cv.ID, id, u.ID, store.CommentThreadUpdate{Version: detail.Thread.Version, Anchor: &store.CommentAnchor{X: 0, Y: 0, ElementID: "new", U: .5, V: .5}}, false); err != nil {
		t.Fatal(err)
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, u.ID, true, 0)
	if err != nil || detail.Thread.Anchor.X != 120 || detail.Thread.Anchor.Y != 220 {
		t.Fatal("rebind to lower-version element", detail.Thread.Anchor, err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO canvas_events(canvas_id,elements) VALUES($1,'[{"id":"new","x":200,"y":300,"width":40,"height":40,"version":2,"versionNonce":1,"isDeleted":true},{"id":"invalid","x":1e999,"y":0,"width":20,"height":20,"version":99999999999999999999999999}]')`, cv.ID); err != nil {
		t.Fatal("comment trigger must tolerate invalid unrelated geometry", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE canvas_scenes SET data='{"elements":[]}' WHERE canvas_id=$1`, cv.ID); err != nil {
		t.Fatal(err)
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, u.ID, true, 0)
	if err != nil || detail.Thread.Anchor.X != 220 || detail.Thread.Anchor.Y != 320 {
		t.Fatal("deleted fallback after rebinding", detail.Thread.Anchor, err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO comments(thread_id,author_id,body,request_id,request_hash) SELECT $1,$2,'reply '||n,gen_random_uuid(),'fixture' FROM generate_series(1,62) n`, id, u.ID); err != nil {
		t.Fatal(err)
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, u.ID, true, 0)
	if err != nil || len(detail.Comments) != 51 || detail.NextBefore == 0 {
		t.Fatal("latest page", len(detail.Comments), detail.NextBefore, err)
	}
	older, err := db.GetCommentThread(ctx, cv.ID, id, u.ID, true, detail.NextBefore)
	if err != nil || len(older.Comments) != 13 || older.NextBefore != 0 {
		t.Fatal("older page", len(older.Comments), older.NextBefore, err)
	}
	if !older.Comments[0].IsRoot || !detail.Comments[0].IsRoot || older.Comments[0].ID != detail.Comments[0].ID {
		t.Fatal("root must be retained on every page")
	}
	if older.Comments[len(older.Comments)-1].Seq >= detail.Comments[1].Seq {
		t.Fatal("reply pages overlap")
	}
}

func TestCommentMigrationRollbackPreservesAccessNotifications(t *testing.T) {
	ctx := context.Background()
	url := testdb.URL(t)
	db, err := store.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	u, err := db.CreateUser(ctx, store.NewUser{PhoneCipher: []byte("rollback"), PhoneHash: []byte("rollback"), PhoneMasked: "138****0000", CountryCode: "+86", PasswordHash: []byte("hash"), Nickname: "rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO notifications(recipient_id,type,event_key,source_version) VALUES($1,'invitation',NULL,NULL),($1,'comment_mentioned','comment:fixture',1)`, u.ID); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/0007_comments.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(down)); err != nil {
		t.Fatal("rollback", err)
	}
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE type='invitation'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("access notification removed", count, err)
	}
	up, err := os.ReadFile("../../migrations/0007_comments.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(up)); err != nil {
		t.Fatal("reapply", err)
	}
}

func TestCommentLifecycleAndConcurrentReads(t *testing.T) {
	ctx := context.Background()
	url := testdb.URL(t)
	db, err := store.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	user := func(name string) store.User {
		u, e := db.CreateUser(ctx, store.NewUser{PhoneCipher: []byte(name), PhoneHash: []byte(name), PhoneMasked: "138****0000", CountryCode: "+86", PasswordHash: []byte("hash"), Nickname: name})
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	owner, viewer, admin, outsider := user("owner"), user("viewer"), user("admin"), user("outsider")
	cv, err := db.CreateCanvas(ctx, owner.ID, "comments")
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateCanvas(ctx, owner.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]string{viewer.ID: "viewer", admin.ID: "admin"} {
		if e := db.AddMember(ctx, cv.WorkspaceID, id, role); e != nil {
			t.Fatal(e)
		}
	}
	input := store.CommentInput{Body: "👨‍👩‍👧‍👦 hello @viewer", RequestID: "11111111-1111-4111-8111-111111111111", Anchor: store.CommentAnchor{X: 10, Y: 20, ElementID: "rect", U: .5, V: .5}, Mentions: []store.CommentMention{{UserID: viewer.ID, Start: 18, End: 25}}}
	// JS string offsets: family is 11 UTF-16 code units, followed by " hello ".
	id, err := db.CreateComment(ctx, cv.ID, "", owner.ID, input)
	if err != nil {
		t.Fatal("create", err)
	}
	duplicate, err := db.CreateComment(ctx, cv.ID, "", owner.ID, input)
	if err != nil || duplicate != id {
		t.Fatal("idempotency", duplicate, err)
	}
	input.Body = "different"
	input.Mentions = nil
	if _, e := db.CreateComment(ctx, cv.ID, "", owner.ID, input); !errors.Is(e, store.ErrCommentConflict) {
		t.Fatal("same request reused", e)
	}
	input.RequestID = "22222222-2222-4222-8222-222222222222"
	if _, e := db.CreateComment(ctx, cv.ID, "", outsider.ID, input); !errors.Is(e, store.ErrCommentNotFound) {
		t.Fatal("outsider", e)
	}
	notifications, unread, err := db.ListNotifications(ctx, viewer.ID, 0)
	if err != nil || unread != 1 || len(notifications) != 1 || notifications[0].Type != "comment_mentioned" {
		t.Fatal("mention notification", notifications, unread, err)
	}
	detail, err := db.GetCommentThread(ctx, cv.ID, id, viewer.ID, false, 0)
	if err != nil || len(detail.Comments) != 1 || !detail.Thread.IsUnread {
		t.Fatal("detail", detail, err)
	}
	root := detail.Comments[0].ID
	seen := detail.Thread
	if e := db.UpdateComment(ctx, cv.ID, id, root, viewer.ID, "hijack", nil, seen.Version, false); !errors.Is(e, store.ErrCommentForbidden) {
		t.Fatal("non-author edit", e)
	}
	if e := db.SetCommentReaction(ctx, other.ID, id, root, viewer.ID, "👍", false); !errors.Is(e, store.ErrCommentNotFound) {
		t.Fatal("cross-canvas", e)
	}
	if e := db.SetCommentReaction(ctx, cv.ID, id, root, viewer.ID, "😀😀", false); !errors.Is(e, store.ErrCommentInvalid) {
		t.Fatal("emoji validation", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for _, u := range []string{owner.ID, viewer.ID, viewer.ID} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			results <- db.SetCommentReaction(ctx, cv.ID, id, root, u, "👍🏽", false)
		}(u)
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, viewer.ID, false, 0)
	if err != nil || len(detail.Comments[0].Reactions) != 1 || len(detail.Comments[0].Reactions[0].Users) != 2 || detail.Thread.UnreadVersion != seen.UnreadVersion {
		t.Fatal("concurrent reactions", detail, err)
	}
	if e := db.SetCommentReaction(ctx, cv.ID, id, root, viewer.ID, "👍🏽", true); e != nil {
		t.Fatal(e)
	}
	reply := store.CommentInput{Body: "🇨🇳 thanks", RequestID: "33333333-3333-4333-8333-333333333333"}
	if _, e := db.CreateComment(ctx, cv.ID, id, admin.ID, reply); e != nil {
		t.Fatal("viewer-role reply", e)
	}
	if e := db.ReadCommentThread(ctx, cv.ID, id, viewer.ID, seen.UnreadVersion, seen.Version); e != nil {
		t.Fatal(e)
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, viewer.ID, false, 0)
	if err != nil || !detail.Thread.IsUnread {
		t.Fatal("new reply incorrectly marked read", detail, err)
	}
	notifications, unread, err = db.ListNotifications(ctx, viewer.ID, 0)
	if err != nil || unread != 0 {
		t.Fatal("not a participant yet: only old mention cleared", unread, notifications, err)
	}
	if e := db.ReadCommentThread(ctx, cv.ID, id, viewer.ID, detail.Thread.UnreadVersion, detail.Thread.Version); e != nil {
		t.Fatal(e)
	}
	resolved := true
	if e := db.UpdateCommentThread(ctx, cv.ID, id, viewer.ID, store.CommentThreadUpdate{Version: detail.Thread.Version, Resolved: &resolved}, false); !errors.Is(e, store.ErrCommentForbidden) {
		t.Fatal("viewer resolved other's thread", e)
	}
	if e := db.UpdateCommentThread(ctx, cv.ID, id, admin.ID, store.CommentThreadUpdate{Version: detail.Thread.Version, Resolved: &resolved}, false); e != nil {
		t.Fatal("admin resolve", e)
	}
	reply.RequestID = "44444444-4444-4444-8444-444444444444"
	if _, e := db.CreateComment(ctx, cv.ID, id, viewer.ID, reply); !errors.Is(e, store.ErrCommentConflict) {
		t.Fatal("reply to resolved", e)
	}
	page, err := db.ListCommentThreads(ctx, cv.ID, viewer.ID, false, store.CommentListOptions{Sort: "date", Limit: 50})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("resolved should be hidden", page, err)
	}
	page, err = db.ListCommentThreads(ctx, cv.ID, viewer.ID, false, store.CommentListOptions{Sort: "unread", IncludeResolved: true, Search: "thanks", Limit: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("reply search", page, err)
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, owner.ID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	resolved = false
	if e := db.UpdateCommentThread(ctx, cv.ID, id, owner.ID, store.CommentThreadUpdate{Version: detail.Thread.Version, Resolved: &resolved}, false); e != nil {
		t.Fatal(e)
	}
	// The geometry fallback survives scene removal, and old frames cannot rewind it.
	for _, e := range []string{`[{"id":"rect","x":100,"y":200,"width":100,"height":60,"angle":0,"version":3,"versionNonce":10,"isDeleted":true}]`, `[{"id":"rect","x":0,"y":0,"width":100,"height":60,"version":2,"versionNonce":1}]`} {
		if _, err = conn.Exec(ctx, `INSERT INTO canvas_events(canvas_id,elements) VALUES($1,$2)`, cv.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, owner.ID, true, 0)
	if err != nil || detail.Thread.Anchor.X != 150 || detail.Thread.Anchor.Y != 230 {
		t.Fatal("anchor fallback", detail.Thread.Anchor, err)
	}
	if e := db.UpdateComment(ctx, cv.ID, id, root, owner.ID, "", nil, detail.Thread.Version, true); e != nil {
		t.Fatal(e)
	}
	detail, err = db.GetCommentThread(ctx, cv.ID, id, viewer.ID, false, 0)
	if err != nil || detail.Comments[0].DeletedAt == nil || detail.Comments[0].Body != "" || len(detail.Comments[0].Reactions) != 0 || len(detail.Comments) != 2 {
		t.Fatal("root deletion should preserve replies", detail, err)
	}
	if e := db.ReadAllCommentThreads(ctx, cv.ID, viewer.ID); e != nil {
		t.Fatal(e)
	}
	page, err = db.ListCommentThreads(ctx, cv.ID, viewer.ID, false, store.CommentListOptions{Sort: "unread", Limit: 50})
	if err != nil || page.Unread != 0 {
		t.Fatal("all read", page, err)
	}
	if e := db.UpdateCommentThread(ctx, cv.ID, id, viewer.ID, store.CommentThreadUpdate{Version: detail.Thread.Version}, true); !errors.Is(e, store.ErrCommentForbidden) {
		t.Fatal("viewer thread removal", e)
	}
	if e := db.UpdateCommentThread(ctx, cv.ID, id, admin.ID, store.CommentThreadUpdate{Version: detail.Thread.Version}, true); e != nil {
		t.Fatal(e)
	}
	if _, e := db.GetCommentThread(ctx, cv.ID, id, viewer.ID, false, 0); !errors.Is(e, store.ErrCommentNotFound) {
		t.Fatal("deleted thread readable", e)
	}
	// Receipts are independent for each instance and do not skip late commits.
	var consumer string
	if e := conn.QueryRow(ctx, `INSERT INTO collaboration_consumers DEFAULT VALUES RETURNING id`).Scan(&consumer); e != nil {
		t.Fatal(e)
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	var late int64
	if e = tx.QueryRow(ctx, `INSERT INTO collaboration_outbox(type) VALUES('test') RETURNING id`).Scan(&late); e != nil {
		t.Fatal(e)
	}
	conn2, e := pgx.Connect(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer conn2.Close(ctx)
	var early int64
	if e = conn2.QueryRow(ctx, `INSERT INTO collaboration_outbox(type) VALUES('test') RETURNING id`).Scan(&early); e != nil {
		t.Fatal(e)
	}
	if _, e = conn2.Exec(ctx, `INSERT INTO collaboration_outbox_receipts(consumer_id,event_id) VALUES($1,$2)`, consumer, early); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	var pending bool
	if e = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM collaboration_outbox o WHERE o.id=$2 AND NOT EXISTS(SELECT 1 FROM collaboration_outbox_receipts r WHERE r.consumer_id=$1 AND r.event_id=o.id))`, consumer, late).Scan(&pending); e != nil || !pending {
		t.Fatal("late transaction lost", e)
	}
}

func TestCommentPaginationAndReadScope(t *testing.T) {
	ctx := context.Background()
	db, err := store.New(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	user := func(name string) store.User {
		u, e := db.CreateUser(ctx, store.NewUser{PhoneCipher: []byte(name), PhoneHash: []byte(name), PhoneMasked: "138****0000", CountryCode: "+86", PasswordHash: []byte("hash"), Nickname: name})
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	owner, viewer := user("owner"), user("viewer")
	a, e := db.CreateCanvas(ctx, owner.ID, "A")
	if e != nil {
		t.Fatal(e)
	}
	b, e := db.CreateCanvas(ctx, owner.ID, "B")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AddMember(ctx, a.WorkspaceID, viewer.ID, "viewer"); e != nil {
		t.Fatal(e)
	}
	for i, cv := range []store.Canvas{a, a, a, b} {
		_, e = db.CreateComment(ctx, cv.ID, "", owner.ID, store.CommentInput{Body: "@viewer", Mentions: []store.CommentMention{{UserID: viewer.ID, Start: 0, End: 7}}, RequestID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)})
		if e != nil {
			t.Fatal(e)
		}
	}
	opts := store.CommentListOptions{Sort: "unread", Limit: 2}
	p, e := db.ListCommentThreads(ctx, a.ID, viewer.ID, false, opts)
	if e != nil || len(p.Items) != 2 || p.NextCursor == "" || p.Total != 3 || p.Unread != 3 {
		t.Fatal("first page", p, e)
	}
	opts.Cursor = p.NextCursor
	q, e := db.ListCommentThreads(ctx, a.ID, viewer.ID, false, opts)
	if e != nil || len(q.Items) != 1 || q.Items[0].ID == p.Items[0].ID {
		t.Fatal("next page", q, e)
	}
	if e = db.ReadAllCommentThreads(ctx, a.ID, viewer.ID); e != nil {
		t.Fatal(e)
	}
	n, unread, e := db.ListNotifications(ctx, viewer.ID, 0)
	if e != nil || unread != 1 {
		t.Fatal("other canvas notification cleared", unread, n, e)
	}
	q, e = db.ListCommentThreads(ctx, b.ID, viewer.ID, false, store.CommentListOptions{Sort: "date", Limit: 50})
	if e != nil || q.Unread != 1 {
		t.Fatal("other canvas reads changed", q, e)
	}
}
