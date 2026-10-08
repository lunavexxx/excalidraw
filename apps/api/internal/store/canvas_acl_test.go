package store

import "testing"

// 角色矩阵:与 M3 方案 §三 一致——owner 最高;ws owner/admin/editor 与
// 协作者 editor → 内容 editor;ws viewer / 协作者 viewer → viewer;
// 无任何关系 → 无权限。信息类操作不走此解析(见 handler RoleOwner 门控)。
func TestResolveCanvasRole(t *testing.T) {
	const owner = "u-owner"
	const member = "u-member"

	cases := []struct {
		name   string
		userID string
		wsRole string
		ccRole string
		want   CanvasRole
	}{
		{"画布 owner 压倒一切", owner, "", "", RoleOwner},
		{"owner 即使 ws viewer 仍是 owner", owner, "viewer", "", RoleOwner},
		{"ws owner → 内容 editor", member, "owner", "", RoleEditor},
		{"ws admin → 内容 editor", member, "admin", "", RoleEditor},
		{"ws editor → 内容 editor", member, "editor", "", RoleEditor},
		{"ws viewer → 内容 viewer", member, "viewer", "", RoleViewer},
		{"协作者 editor(无 ws 关系)", member, "", "editor", RoleEditor},
		{"协作者 viewer(无 ws 关系)", member, "", "viewer", RoleViewer},
		{"ws viewer + 协作者 editor → editor 取高", member, "viewer", "editor", RoleEditor},
		{"ws editor + 协作者 viewer → editor 取高", member, "editor", "viewer", RoleEditor},
		{"完全无关 → 无权限", member, "", "", RoleNone},
		{"ws 角色为空串 → 无权限", member, "", "", RoleNone},
	}
	for _, tc := range cases {
		if got := ResolveCanvasRole(owner, tc.userID, tc.wsRole, tc.ccRole); got != tc.want {
			t.Fatalf("%s: ResolveCanvasRole = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCanvasRoleOrdering(t *testing.T) {
	if !RoleOwner.AtLeast(RoleOwner) || !RoleOwner.AtLeast(RoleEditor) || !RoleOwner.AtLeast(RoleViewer) {
		t.Fatal("owner 应满足全部最低角色")
	}
	if !RoleEditor.AtLeast(RoleViewer) || !RoleEditor.AtLeast(RoleEditor) {
		t.Fatal("editor 应满足 viewer/editor 门槛")
	}
	if RoleEditor.AtLeast(RoleOwner) {
		t.Fatal("editor 不应满足 owner 门槛")
	}
	if RoleViewer.AtLeast(RoleEditor) {
		t.Fatal("viewer 不应满足 editor 门槛")
	}
	if RoleNone.AtLeast(RoleViewer) {
		t.Fatal("无权限不应满足 viewer 门槛")
	}
}

func TestCanManageCanvas(t *testing.T) {
	for _, tc := range []struct {
		user, ws string
		want     bool
	}{
		{"owner", "", true}, {"member", "owner", true}, {"member", "admin", true},
		{"member", "editor", false}, {"member", "viewer", false}, {"member", "", false}, {"", "admin", false},
	} {
		if got := CanManageCanvas("owner", tc.user, tc.ws); got != tc.want {
			t.Fatalf("user=%q ws=%q: got %v", tc.user, tc.ws, got)
		}
	}
}
