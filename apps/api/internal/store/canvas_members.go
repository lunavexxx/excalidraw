package store

import "context"

// Management is separate from content roles: workspace admins do not own canvases.
func CanManageCanvas(ownerID, userID, workspaceRole string) bool {
	return userID != "" && (ownerID == userID || workspaceRole == "owner" || workspaceRole == "admin")
}

type CanvasCapabilities struct {
	ManageCollaborators bool `json:"can_manage_collaborators"`
	ManageShareLinks    bool `json:"can_manage_share_links"`
	ReviewRequests      bool `json:"can_review_requests"`
}

func (db *DB) CanvasCapabilities(ctx context.Context, canvas Canvas, userID string) (CanvasCapabilities, error) {
	if userID == "" || canvas.ID == "" {
		return CanvasCapabilities{}, nil
	}
	var wsRole string
	err := db.pool.QueryRow(ctx, `SELECT COALESCE((SELECT role FROM workspace_members WHERE workspace_id=$1 AND user_id=$2),'')`, canvas.WorkspaceID, userID).Scan(&wsRole)
	if err != nil {
		return CanvasCapabilities{}, err
	}
	manage := CanManageCanvas(canvas.OwnerID, userID, wsRole)
	return CanvasCapabilities{manage, manage, manage}, nil
}

type CanvasMember struct {
	UserID        string     `json:"user_id"`
	Nickname      string     `json:"nickname"`
	AvatarURL     string     `json:"avatar_url"`
	PhoneMasked   string     `json:"phone_masked,omitempty"`
	EffectiveRole CanvasRole `json:"effective_role"`
	DirectRole    string     `json:"direct_role"`
	WorkspaceRole string     `json:"workspace_role"`
	CanManage     bool       `json:"can_manage"`
}

// Keyset paging includes inherited members and the owner, with one row per account.
func (db *DB) ListCanvasMembers(ctx context.Context, canvas Canvas, after, search string, limit int, management bool) ([]CanvasMember, string, error) {
	rows, err := db.pool.Query(ctx, `
 WITH members AS (
 SELECT $2::uuid AS user_id UNION SELECT user_id FROM workspace_members WHERE workspace_id=$3
 UNION SELECT user_id FROM canvas_collaborators WHERE canvas_id=$1
 )
 SELECT u.id, u.nickname, COALESCE(u.avatar_url,''), u.phone_masked, COALESCE(cc.role,''), COALESCE(wm.role,'')
 FROM members m JOIN users u ON u.id=m.user_id
 LEFT JOIN canvas_collaborators cc ON cc.canvas_id=$1 AND cc.user_id=u.id
 LEFT JOIN workspace_members wm ON wm.workspace_id=$3 AND wm.user_id=u.id
 WHERE ($4='' OR u.id::text > $4) AND ($5='' OR strpos(lower(u.nickname),lower($5))>0)
 ORDER BY u.id LIMIT $6`, canvas.ID, canvas.OwnerID, canvas.WorkspaceID, after, search, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []CanvasMember{}
	for rows.Next() {
		var m CanvasMember
		if err := rows.Scan(&m.UserID, &m.Nickname, &m.AvatarURL, &m.PhoneMasked, &m.DirectRole, &m.WorkspaceRole); err != nil {
			return nil, "", err
		}
		m.EffectiveRole = ResolveCanvasRole(canvas.OwnerID, m.UserID, m.WorkspaceRole, m.DirectRole)
		m.CanManage = CanManageCanvas(canvas.OwnerID, m.UserID, m.WorkspaceRole)
		if !management {
			m.PhoneMasked = ""
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].UserID
	}
	return items, next, nil
}
