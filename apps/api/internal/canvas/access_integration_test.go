package canvas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"github.com/lunavexxx/excalidraw/apps/api/internal/testdb"
	"net/http/httptest"
	"testing"
)

func TestAccessRoutesIntegration(t *testing.T) {
	db, err := store.New(context.Background(), testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	pc, err := auth.NewPhoneCrypto(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	createUser := func(phone, name string) store.User {
		cipher, _ := pc.Seal(phone)
		u, err := db.CreateUser(context.Background(), store.NewUser{PhoneCipher: cipher, PhoneHash: pc.Hash(phone), PhoneMasked: auth.MaskPhone(phone), CountryCode: "+86", PasswordHash: []byte("hash"), Nickname: name})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner := createUser("13800000001", "owner")
	admin := createUser("13800000002", "admin")
	viewer := createUser("13800000003", "viewer")
	outsider := createUser("13800000004", "outsider")
	cv, err := db.CreateCanvas(context.Background(), owner.ID, "canvas")
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]string{admin.ID: "admin", viewer.ID: "viewer"} {
		if err := db.AddMember(context.Background(), cv.WorkspaceID, id, role); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), db, "secret", DefaultLimits(), pc)
	call := func(userID, method, path string, payload any, entry string) struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	} { t.Helper(); raw, _ := json.Marshal(payload); req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(raw)); token, _, _ := auth.SignAccessToken("secret", userID); req.Header.Set("Authorization", "Bearer "+token); req.Header.Set("Content-Type", "application/json"); req.Header.Set("X-Access-Entry", entry); rec := httptest.NewRecorder(); router.ServeHTTP(rec, req); var body struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}; if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}; return body }
	path := "/canvases/" + cv.ID
	body := call(admin.ID, "GET", path+"/members", nil, "")
	if body.Code != 0 {
		t.Fatalf("admin members: %s", body.Data)
	}
	body = call(viewer.ID, "PUT", path+"/collaborators", map[string]string{"phone": "13800000004", "role": "editor"}, "")
	if body.Code != 42002 {
		t.Fatalf("viewer mutated collaborators: %d", body.Code)
	}
	body = call(admin.ID, "PUT", path+"/collaborators", map[string]string{"phone": "13800000004", "role": "viewer"}, "")
	if body.Code != 0 {
		t.Fatalf("admin cannot grant: %d %s", body.Code, body.Data)
	}
	body = call(viewer.ID, "GET", path+"/members", nil, "")
	if body.Code != 0 || bytes.Contains(body.Data, []byte("phone_masked")) {
		t.Fatal("member display leaked management data", string(body.Data))
	}
	body = call(admin.ID, "DELETE", path+"/collaborators/"+outsider.ID, nil, "")
	if body.Code != 0 {
		t.Fatal("remove", body.Code)
	}
	body = call(outsider.ID, "GET", path+"/members", nil, "")
	if body.Code != 41001 {
		t.Fatal("unauthorized members", body.Code)
	}
	body = call(admin.ID, "POST", path+"/access-entries", nil, "")
	if body.Code != 0 {
		t.Fatal("create request link", body.Code)
	}
	var entry struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(body.Data, &entry) != nil || entry.Token == "" {
		t.Fatal("entry token missing")
	}
	body = call(outsider.ID, "POST", path+"/access-requests", map[string]string{"role": "editor", "reason": "please"}, entry.Token)
	if body.Code != 0 {
		t.Fatal("entry request", body.Code, string(body.Data))
	}
	var r store.AccessRequest
	if err := json.Unmarshal(body.Data, &r); err != nil {
		t.Fatal(err)
	}
	body = call(outsider.ID, "GET", path+"/access-requests", nil, entry.Token)
	if body.Code != 0 {
		t.Fatal("own request list", body.Code, string(body.Data))
	}
	body = call(viewer.ID, "POST", path+"/access-requests/"+r.ID+"/decision", map[string]string{"decision": "approved", "role": "editor"}, "")
	if body.Code != 42002 {
		t.Fatal("viewer approved", body.Code)
	}
	body = call(admin.ID, "POST", path+"/access-requests/"+r.ID+"/decision", map[string]string{"decision": "approved", "role": "editor"}, "")
	if body.Code != 0 {
		t.Fatal("admin approval", body.Code)
	}
	body = call(outsider.ID, "GET", path, nil, "")
	if body.Code != 0 || !bytes.Contains(body.Data, []byte(`"my_role":"editor"`)) {
		t.Fatal("approved user cannot load", string(body.Data))
	}
	body = call(admin.ID, "POST", path+"/invitations", map[string]string{"phone": "13800000005", "role": "viewer"}, "")
	if body.Code != 0 {
		t.Fatal("unregistered invitation", body.Code)
	}
	var invitation struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body.Data, &invitation); err != nil {
		t.Fatal(err)
	}
	body = call(outsider.ID, "POST", "/invitations/accept", map[string]string{"token": invitation.Token}, "")
	if body.Code != 42002 {
		t.Fatal("wrong invitation identity", body.Code)
	}
	invited := createUser("13800000005", "invited")
	body = call(invited.ID, "POST", "/invitations/accept", map[string]string{"token": invitation.Token}, "")
	if body.Code != 0 {
		t.Fatal("accept", body.Code)
	}
}
