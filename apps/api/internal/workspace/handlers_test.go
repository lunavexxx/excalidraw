package workspace

import (
	"testing"

	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

func TestWorkspaceRoleAtLeast(t *testing.T) {
	cases := []struct {
		role, min string
		want      bool
	}{
		{"owner", "viewer", true},
		{"owner", "admin", true},
		{"admin", "admin", true},
		{"admin", "owner", false},
		{"editor", "editor", true},
		{"editor", "admin", false},
		{"viewer", "viewer", true},
		{"viewer", "editor", false},
		{"", "viewer", false},
	}
	for _, tc := range cases {
		if got := store.WorkspaceRoleAtLeast(tc.role, tc.min); got != tc.want {
			t.Fatalf("WorkspaceRoleAtLeast(%q, %q) = %v, want %v", tc.role, tc.min, got, tc.want)
		}
	}
}

func TestValidWorkspaceName(t *testing.T) {
	if !validName("团队 A") || !validName(string(make([]rune, 100))) {
		t.Fatal("valid names should pass")
	}
	if validName("") || validName("   ") || validName(string(make([]rune, 101))) {
		t.Fatal("invalid names should fail")
	}
}

func TestValidWorkspaceRole(t *testing.T) {
	for _, ok := range []string{"admin", "editor", "viewer"} {
		if !validWorkspaceRole(ok) {
			t.Fatalf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"owner", "", "Admin", "guest"} {
		if validWorkspaceRole(bad) {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}
