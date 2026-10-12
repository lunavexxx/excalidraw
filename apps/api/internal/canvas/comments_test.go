package canvas

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"github.com/lunavexxx/excalidraw/apps/api/internal/testdb"
)

func TestCommentHTTPPermissionMatrix(t *testing.T) {
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
	owner, viewer, outsider := user("owner"), user("viewer"), user("outsider")
	cv, e := db.CreateCanvas(ctx, owner.ID, "comments")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AddMember(ctx, cv.WorkspaceID, viewer.ID, "viewer"); e != nil {
		t.Fatal(e)
	}
	link, e := db.CreateShareLink(ctx, cv.ID, []byte("comment-guest-hash"), "viewer", owner.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	guest, _, e := auth.SignGuestToken("secret", link.ID, cv.ID, "viewer", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	token := func(id string) string {
		s, _, e := auth.SignAccessToken("secret", id)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), db, "secret", DefaultLimits(), nil)
	call := func(tk, method, path string, payload any) (int, json.RawMessage) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+tk)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var b struct {
			Code int
			Data json.RawMessage
		}
		if e := json.Unmarshal(rec.Body.Bytes(), &b); e != nil {
			t.Fatal(e, rec.Body.String())
		}
		return b.Code, b.Data
	}
	path := "/canvases/" + cv.ID + "/comment-threads"
	input := map[string]any{"body": "👍🏽", "request_id": "11111111-1111-4111-8111-111111111111", "anchor": map[string]int{"x": 1, "y": 2}}
	if code, _ := call(guest, "POST", path, input); code != 42002 {
		t.Fatal("guest write", code)
	}
	if code, _ := call(token(outsider.ID), "POST", path, input); code != 41001 {
		t.Fatal("outsider write", code)
	}
	code, data := call(token(viewer.ID), "POST", path, input)
	if code != 0 {
		t.Fatal("viewer creation", code, string(data))
	}
	var created struct {
		ID string `json:"thread_id"`
	}
	if e = json.Unmarshal(data, &created); e != nil {
		t.Fatal(e)
	}
	if code, _ = call(guest, "GET", path+"/"+created.ID, nil); code != 0 {
		t.Fatal("guest read", code)
	}
	if code, _ = call(guest, "POST", path+"/"+created.ID+"/read", map[string]int{"version": 1, "unread_version": 1}); code != 42002 {
		t.Fatal("guest receipt", code)
	}
	if code, _ = call(token(outsider.ID), "GET", path+"/"+created.ID, nil); code != 41001 {
		t.Fatal("outsider read", code)
	}
	if code, _ = call(token(viewer.ID), "POST", path+"/read-all", nil); code != 0 {
		t.Fatal("read-all route", code)
	}
	if code, _ = call(token(viewer.ID), "PATCH", path+"/"+created.ID, map[string]any{"version": 1, "resolved": true}); code != 0 {
		t.Fatal("creator resolution", code)
	}
	if code, _ = call(token(viewer.ID), "DELETE", path+"/"+created.ID, map[string]int{"version": 2}); code != 42002 {
		t.Fatal("creator thread deletion", code)
	}
	if code, _ = call(token(owner.ID), "DELETE", path+"/"+created.ID, map[string]int{"version": 2}); code != 0 {
		t.Fatal("owner thread deletion", code)
	}
	if code, _ = call(guest, "GET", path+"/"+created.ID, nil); code != 43001 {
		t.Fatal("deleted read", code)
	}
}
