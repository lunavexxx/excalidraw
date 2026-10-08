package store_test

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"github.com/lunavexxx/excalidraw/apps/api/internal/testdb"
	"sync"
	"testing"
)

func TestCollaborationAccessLifecycle(t *testing.T) {
	ctx := context.Background()
	testURL := testdb.URL(t)
	db, err := store.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	user := func(name string) store.User {
		u, err := db.CreateUser(ctx, store.NewUser{PhoneCipher: []byte(name), PhoneHash: []byte(name), PhoneMasked: "138****0000", CountryCode: "+86", PasswordHash: []byte("hash"), Nickname: name})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, admin, viewer, outsider := user("owner"), user("admin"), user("viewer"), user("outsider")
	canvas, err := db.CreateCanvas(ctx, owner.ID, "test canvas")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddMember(ctx, canvas.WorkspaceID, admin.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddMember(ctx, canvas.WorkspaceID, viewer.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	caps, err := db.CanvasCapabilities(ctx, canvas, admin.ID)
	if err != nil || !caps.ManageCollaborators {
		t.Fatalf("admin caps=%+v err=%v", caps, err)
	}
	caps, err = db.CanvasCapabilities(ctx, canvas, viewer.ID)
	if err != nil || caps.ManageCollaborators {
		t.Fatalf("viewer caps=%+v err=%v", caps, err)
	}
	people, cursor, err := db.ListCanvasMembers(ctx, canvas, "", "", 2, false)
	if err != nil || len(people) != 2 || cursor == "" {
		t.Fatalf("members=%+v cursor=%q err=%v", people, cursor, err)
	}
	rest, _, err := db.ListCanvasMembers(ctx, canvas, cursor, "", 2, false)
	if err != nil || len(rest) != 1 {
		t.Fatal("member paging failed", err)
	}
	for _, m := range append(people, rest...) {
		if m.PhoneMasked != "" {
			t.Fatal("reader sees phone")
		}
	}
	r, err := db.CreateAccessRequest(ctx, canvas.ID, outsider.ID, "editor", "please")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := db.CreateAccessRequest(ctx, canvas.ID, outsider.ID, "editor", "duplicate")
	if err != nil || duplicate.ID != r.ID {
		t.Fatal("duplicate submission", err)
	}
	n, unread, err := db.ListNotifications(ctx, admin.ID, 0)
	if err != nil || unread != 1 || len(n) != 1 {
		t.Fatalf("admin notification=%+v unread=%d err=%v", n, unread, err)
	}
	// Two managers cannot decide the same pending request twice.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, actor := range []string{owner.ID, admin.ID} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			results <- db.DecideAccessRequest(ctx, canvas.ID, r.ID, actor, "approved", "editor", "", true)
		}(actor)
	}
	wg.Wait()
	close(results)
	success, unavailable := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, store.ErrAccessState) {
			unavailable++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || unavailable != 1 {
		t.Fatalf("approval results %d/%d", success, unavailable)
	}
	role, _, err := db.AuthorizeCanvas(ctx, store.AccessIdentity{UserID: outsider.ID}, canvas.ID)
	if err != nil || role != store.RoleEditor {
		t.Fatalf("role=%s err=%v", role, err)
	}
	n, unread, err = db.ListNotifications(ctx, outsider.ID, 0)
	if err != nil || unread != 1 || len(n) != 1 || n[0].Type != "access_approved" {
		t.Fatal("approval notification", n, err)
	}
	if err := db.ReadNotifications(ctx, viewer.ID, n[0].ID); err != nil {
		t.Fatal(err)
	}
	_, unread, _ = db.ListNotifications(ctx, outsider.ID, 0)
	if unread != 1 {
		t.Fatal("another user read a notification")
	}
	if err := db.ReadNotifications(ctx, outsider.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, unread, _ = db.ListNotifications(ctx, outsider.ID, 0)
	if unread != 0 {
		t.Fatal("mark read failed")
	}
	// Pending upgrade remains when a viewer grant is insufficient, then auto-closes on editor grant.
	vr, err := db.CreateAccessRequest(ctx, canvas.ID, viewer.ID, "editor", "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.GrantCollaborator(ctx, canvas.ID, viewer.ID, "viewer", owner.ID); err != nil {
		t.Fatal(err)
	}
	reqs, err := db.ListAccessRequests(ctx, canvas.ID, viewer.ID, false)
	if err != nil || reqs[0].Status != "pending" {
		t.Fatal("insufficient grant closed request", err)
	}
	if err := db.GrantCollaborator(ctx, canvas.ID, viewer.ID, "editor", owner.ID); err != nil {
		t.Fatal(err)
	}
	reqs, err = db.ListAccessRequests(ctx, canvas.ID, viewer.ID, false)
	if err != nil || reqs[0].ID != vr.ID || reqs[0].Status != "approved" {
		t.Fatal("direct grant did not close request", err)
	}
	// Invitation can be created before registration; wrong account cannot accept.
	invitation, err := db.CreateInvitation(ctx, canvas.ID, "viewer", owner.ID, "138****0000", []byte("newuser"), []byte("cipher"), []byte("invite-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcceptInvitation(ctx, outsider.ID, []byte("invite-token")); !errors.Is(err, store.ErrInviteIdentity) {
		t.Fatalf("wrong account accepted: %v", err)
	}
	invited := user("newuser")
	id, err := db.AcceptInvitation(ctx, invited.ID, []byte("invite-token"))
	if err != nil || id != canvas.ID {
		t.Fatal("accept after registration", err)
	}
	if _, err := db.AcceptInvitation(ctx, invited.ID, []byte("invite-token")); err != nil {
		t.Fatal("accept not idempotent", err)
	}
	if _, err := db.InvitationHashForUser(ctx, invitation.ID, outsider.ID); !errors.Is(err, store.ErrInviteIdentity) {
		t.Fatal("invitation notification identity")
	}
	entry, err := db.CreateAccessEntry(ctx, canvas.ID, owner.ID, []byte("request-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.AccessEntryCanvas(ctx, []byte("request-token")); err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeAccessEntry(ctx, canvas.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.AccessEntryCanvas(ctx, []byte("request-token")); !errors.Is(err, store.ErrAccessState) {
		t.Fatal("revoked request entry valid")
	}
	// An old viewer invitation cannot downgrade an existing direct editor.
	if _, err := db.CreateInvitation(ctx, canvas.ID, "viewer", owner.ID, "138****0000", []byte("outsider"), []byte("cipher"), []byte("lower-invite")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcceptInvitation(ctx, outsider.ID, []byte("lower-invite")); err != nil {
		t.Fatal(err)
	}
	role, _, err = db.AuthorizeCanvas(ctx, store.AccessIdentity{UserID: outsider.ID}, canvas.ID)
	if err != nil || role != store.RoleEditor {
		t.Fatal("invitation downgraded editor", err)
	}
	// Expired and revoked invitations do not grant access.
	expired, err := db.CreateInvitation(ctx, canvas.ID, "viewer", owner.ID, "138****0000", []byte("newuser"), []byte("cipher"), []byte("expired-invite"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE canvas_invitations SET expires_at=now()-interval '1 minute' WHERE id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcceptInvitation(ctx, invited.ID, []byte("expired-invite")); !errors.Is(err, store.ErrAccessState) {
		t.Fatal("expired invitation accepted", err)
	}
	revoked, err := db.CreateInvitation(ctx, canvas.ID, "viewer", owner.ID, "138****0000", []byte("newuser"), []byte("cipher"), []byte("revoked-invite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeInvitation(ctx, canvas.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcceptInvitation(ctx, invited.ID, []byte("revoked-invite")); !errors.Is(err, store.ErrAccessState) {
		t.Fatal("revoked invitation accepted", err)
	}
	// Repeating the same direct grant is a no-op, including notifications.
	_, before, err := db.ListNotifications(ctx, viewer.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.GrantCollaborator(ctx, canvas.ID, viewer.ID, "editor", owner.ID); err != nil {
		t.Fatal(err)
	}
	_, after, err := db.ListNotifications(ctx, viewer.ID, 0)
	if err != nil || before != after {
		t.Fatal("duplicate grant notified again", err)
	}
	// Removing a direct grant preserves inherited workspace access.
	if err := db.RemoveCollaborator(ctx, canvas.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	role, _, err = db.AuthorizeCanvas(ctx, store.AccessIdentity{UserID: viewer.ID}, canvas.ID)
	if err != nil || role != store.RoleViewer {
		t.Fatal("inherited access lost", err)
	}
}
