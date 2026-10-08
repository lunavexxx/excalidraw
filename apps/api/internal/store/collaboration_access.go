package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrAccessState = errors.New("access action no longer available")
var ErrInviteIdentity = errors.New("invitation belongs to another account")

type Invitation struct {
	ID          string    `json:"id"`
	CanvasID    string    `json:"canvas_id"`
	CanvasName  string    `json:"canvas_name"`
	PhoneMasked string    `json:"phone_masked"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

const invitationSelect = `SELECT i.id,i.canvas_id,c.name,i.phone_masked,i.role,i.status,i.expires_at,i.created_at FROM canvas_invitations i JOIN canvases c ON c.id=i.canvas_id`

func scanInvitation(row rowScanner) (Invitation, error) {
	var i Invitation
	err := row.Scan(&i.ID, &i.CanvasID, &i.CanvasName, &i.PhoneMasked, &i.Role, &i.Status, &i.ExpiresAt, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrAccessState
	}
	return i, err
}

func notify(ctx context.Context, tx pgx.Tx, recipient, kind, canvasID, entityID, actor string) error {
	_, err := tx.Exec(ctx, `INSERT INTO notifications(recipient_id,type,canvas_id,entity_id,actor_id) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid)`, recipient, kind, canvasID, entityID, actor)
	return err
}
func notifyManagers(ctx context.Context, tx pgx.Tx, canvasID, entityID, actor string) error {
	_, err := tx.Exec(ctx, `INSERT INTO notifications(recipient_id,type,canvas_id,entity_id,actor_id)
 SELECT user_id,'access_requested',$1,$2,$3 FROM (
 SELECT owner_id AS user_id FROM canvases WHERE id=$1 UNION
 SELECT wm.user_id FROM workspace_members wm JOIN canvases c ON c.workspace_id=wm.workspace_id WHERE c.id=$1 AND wm.role IN ('owner','admin')
 ) managers WHERE user_id<>$3`, canvasID, entityID, actor)
	return err
}

func lockCanvas(ctx context.Context, tx pgx.Tx, canvasID string) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM canvases WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, canvasID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccessState
	}
	return err
}

// Grant and any pending-request resolution share one transaction with their notifications.
func grant(ctx context.Context, tx pgx.Tx, canvasID, userID, role, actor string) error {
	_, err := tx.Exec(ctx, `INSERT INTO canvas_collaborators(canvas_id,user_id,role,invited_by) VALUES ($1,$2,$3,$4)
 ON CONFLICT(canvas_id,user_id) DO UPDATE SET role=EXCLUDED.role,invited_by=EXCLUDED.invited_by`, canvasID, userID, role, actor)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `UPDATE canvas_access_requests SET status='approved',granted_role=$3,reviewed_by=$4,updated_at=now()
 WHERE canvas_id=$1 AND user_id=$2 AND status='pending' AND (role='viewer' OR $3='editor') RETURNING id`, canvasID, userID, role, actor)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := notify(ctx, tx, userID, "access_approved", canvasID, id, actor); err != nil {
			return err
		}
	}
	return nil
}
func (db *DB) GrantCollaborator(ctx context.Context, canvasID, userID, role, actor string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCanvas(ctx, tx, canvasID); err != nil {
		return err
	}
	var currentRole string
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT role FROM canvas_collaborators WHERE canvas_id=$1 AND user_id=$2),'')`, canvasID, userID).Scan(&currentRole); err != nil {
		return err
	}
	if currentRole == role {
		return tx.Commit(ctx)
	}
	if err := grant(ctx, tx, canvasID, userID, role, actor); err != nil {
		return err
	}
	if userID != actor {
		if err := notify(ctx, tx, userID, "member_added", canvasID, "", actor); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (db *DB) CreateInvitation(ctx context.Context, canvasID, role, actor, masked string, phoneHash, phoneCipher, tokenHash []byte) (Invitation, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO canvas_invitations(canvas_id,role,invited_by,phone_masked,phone_hash,phone_cipher,token_hash,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,now()+interval '7 days') RETURNING id`, canvasID, role, actor, masked, phoneHash, phoneCipher, tokenHash).Scan(&id)
	if err != nil {
		return Invitation{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO notifications(recipient_id,type,canvas_id,entity_id,actor_id) SELECT id,'invitation',$1,$2,$3 FROM users WHERE phone_hash=$4`, canvasID, id, actor, phoneHash)
	if err != nil {
		return Invitation{}, err
	}
	i, err := scanInvitation(tx.QueryRow(ctx, invitationSelect+` WHERE i.id=$1`, id))
	if err != nil {
		return i, err
	}
	return i, tx.Commit(ctx)
}
func (db *DB) ListInvitations(ctx context.Context, canvasID string) ([]Invitation, error) {
	rows, err := db.pool.Query(ctx, invitationSelect+` WHERE i.canvas_id=$1 ORDER BY i.created_at DESC LIMIT 100`, canvasID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Invitation{}
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
func (db *DB) InvitationByToken(ctx context.Context, hash []byte) (Invitation, error) {
	return scanInvitation(db.pool.QueryRow(ctx, invitationSelect+` WHERE i.token_hash=$1 AND c.deleted_at IS NULL AND i.status='pending' AND i.expires_at>now()`, hash))
}
func (db *DB) RevokeInvitation(ctx context.Context, canvasID, id string) error {
	tag, err := db.pool.Exec(ctx, `UPDATE canvas_invitations SET status='revoked' WHERE canvas_id=$1 AND id=$2 AND status='pending'`, canvasID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrAccessState
	}
	return err
}
func (db *DB) AcceptInvitation(ctx context.Context, userID string, hash []byte) (string, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var targetCanvas string
	if err := tx.QueryRow(ctx, `SELECT canvas_id FROM canvas_invitations WHERE token_hash=$1`, hash).Scan(&targetCanvas); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrAccessState
		}
		return "", err
	}
	if err := lockCanvas(ctx, tx, targetCanvas); err != nil {
		return "", err
	}
	var id, canvasID, role, actor, status string
	var matches, valid bool
	err = tx.QueryRow(ctx, `SELECT i.id,i.canvas_id,i.role,COALESCE(i.invited_by::text,''),i.status,u.phone_hash=i.phone_hash,i.expires_at>now() AND c.deleted_at IS NULL FROM canvas_invitations i JOIN users u ON u.id=$2 JOIN canvases c ON c.id=i.canvas_id WHERE i.token_hash=$1 FOR UPDATE OF i`, hash, userID).Scan(&id, &canvasID, &role, &actor, &status, &matches, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAccessState
	}
	if err != nil {
		return "", err
	}
	if !matches {
		return "", ErrInviteIdentity
	}
	if status == "accepted" {
		return canvasID, nil
	}
	if status != "pending" || !valid || actor == "" {
		return "", ErrAccessState
	}
	var higher bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM canvas_collaborators WHERE canvas_id=$1 AND user_id=$2 AND role='editor')`, canvasID, userID).Scan(&higher)
	if err != nil {
		return "", err
	}
	if higher {
		role = "editor"
	}
	if err := grant(ctx, tx, canvasID, userID, role, actor); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE canvas_invitations SET status='accepted',accepted_by=$2 WHERE id=$1`, id, userID); err != nil {
		return "", err
	}
	if err := notify(ctx, tx, userID, "invitation_accepted", canvasID, id, actor); err != nil {
		return "", err
	}
	return canvasID, tx.Commit(ctx)
}

type AccessEntry struct {
	ID        string     `json:"id"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

func (db *DB) CreateAccessEntry(ctx context.Context, canvasID, actor string, hash []byte) (AccessEntry, error) {
	var e AccessEntry
	err := db.pool.QueryRow(ctx, `INSERT INTO canvas_access_entries(canvas_id,created_by,token_hash,expires_at) VALUES ($1,$2,$3,now()+interval '7 days') RETURNING id,expires_at,revoked_at`, canvasID, actor, hash).Scan(&e.ID, &e.ExpiresAt, &e.RevokedAt)
	return e, err
}
func (db *DB) ListAccessEntries(ctx context.Context, canvasID string) ([]AccessEntry, error) {
	rows, err := db.pool.Query(ctx, `SELECT id,expires_at,revoked_at FROM canvas_access_entries WHERE canvas_id=$1 ORDER BY created_at DESC LIMIT 100`, canvasID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AccessEntry{}
	for rows.Next() {
		var e AccessEntry
		if err := rows.Scan(&e.ID, &e.ExpiresAt, &e.RevokedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}
func (db *DB) RevokeAccessEntry(ctx context.Context, canvasID, id string) error {
	_, err := db.pool.Exec(ctx, `UPDATE canvas_access_entries SET revoked_at=COALESCE(revoked_at,now()) WHERE canvas_id=$1 AND id=$2`, canvasID, id)
	return err
}
func (db *DB) AccessEntryCanvas(ctx context.Context, hash []byte) (string, string, error) {
	var id, name string
	err := db.pool.QueryRow(ctx, `SELECT c.id,c.name FROM canvas_access_entries e JOIN canvases c ON c.id=e.canvas_id WHERE e.token_hash=$1 AND e.revoked_at IS NULL AND e.expires_at>now() AND c.deleted_at IS NULL`, hash).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrAccessState
	}
	return id, name, err
}

type AccessRequest struct {
	ID           string    `json:"id"`
	CanvasID     string    `json:"canvas_id"`
	UserID       string    `json:"user_id"`
	Nickname     string    `json:"nickname"`
	Role         string    `json:"role"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	GrantedRole  string    `json:"granted_role"`
	ResultReason string    `json:"result_reason"`
	CreatedAt    time.Time `json:"created_at"`
}

const requestSelect = `SELECT r.id,r.canvas_id,r.user_id,u.nickname,r.role,r.reason,r.status,COALESCE(r.granted_role,''),r.result_reason,r.created_at FROM canvas_access_requests r JOIN users u ON u.id=r.user_id`

func scanRequest(row rowScanner) (AccessRequest, error) {
	var r AccessRequest
	err := row.Scan(&r.ID, &r.CanvasID, &r.UserID, &r.Nickname, &r.Role, &r.Reason, &r.Status, &r.GrantedRole, &r.ResultReason, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrAccessState
	}
	return r, err
}
func (db *DB) ListAccessRequests(ctx context.Context, canvasID, userID string, management bool) ([]AccessRequest, error) {
	rows, err := db.pool.Query(ctx, requestSelect+` WHERE r.canvas_id=$1 AND ($3 OR r.user_id=$2) ORDER BY r.created_at DESC LIMIT 100`, canvasID, userID, management)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AccessRequest{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
func (db *DB) CreateAccessRequest(ctx context.Context, canvasID, userID, role, reason string) (AccessRequest, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return AccessRequest{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize submissions and grants for this canvas; also recheck inherited permission.
	var owner, wsRole, ccRole string
	err = tx.QueryRow(ctx, `SELECT c.owner_id,COALESCE(wm.role,''),COALESCE(cc.role,'') FROM canvases c LEFT JOIN workspace_members wm ON wm.workspace_id=c.workspace_id AND wm.user_id=$2 LEFT JOIN canvas_collaborators cc ON cc.canvas_id=c.id AND cc.user_id=$2 WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c`, canvasID, userID).Scan(&owner, &wsRole, &ccRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccessRequest{}, ErrAccessState
	}
	if err != nil {
		return AccessRequest{}, err
	}
	if ResolveCanvasRole(owner, userID, wsRole, ccRole).AtLeast(CanvasRole(role)) {
		return AccessRequest{}, ErrAccessState
	}
	r, err := scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE r.canvas_id=$1 AND r.user_id=$2 AND r.status='pending'`, canvasID, userID))
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, ErrAccessState) {
		return r, err
	}
	var recent bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM canvas_access_requests WHERE canvas_id=$1 AND user_id=$2 AND updated_at>now()-interval '1 minute')`, canvasID, userID).Scan(&recent)
	if err != nil {
		return r, err
	}
	if recent {
		return r, ErrAccessState
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO canvas_access_requests(canvas_id,user_id,role,reason) VALUES ($1,$2,$3,$4) RETURNING id`, canvasID, userID, role, reason).Scan(&id)
	if err != nil {
		return r, err
	}
	if err := notifyManagers(ctx, tx, canvasID, id, userID); err != nil {
		return r, err
	}
	r, err = scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE r.id=$1`, id))
	if err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}
func (db *DB) DecideAccessRequest(ctx context.Context, canvasID, id, actor, decision, role, reason string, management bool) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Same lock order as submission and direct grant: canvas, then request.
	var owner, ws string
	err = tx.QueryRow(ctx, `SELECT c.owner_id,COALESCE(wm.role,'') FROM canvases c LEFT JOIN workspace_members wm ON wm.workspace_id=c.workspace_id AND wm.user_id=$2 WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c`, canvasID, actor).Scan(&owner, &ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccessState
	}
	if err != nil {
		return err
	}
	if management && !CanManageCanvas(owner, actor, ws) {
		return ErrAccessState
	}
	var userID, status string
	err = tx.QueryRow(ctx, `SELECT user_id,status FROM canvas_access_requests WHERE id=$1 AND canvas_id=$2 FOR UPDATE`, id, canvasID).Scan(&userID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccessState
	}
	if err != nil {
		return err
	}
	if status != "pending" || (!management && (decision != "cancelled" || userID != actor)) {
		return ErrAccessState
	}
	_, err = tx.Exec(ctx, `UPDATE canvas_access_requests SET status=$2,granted_role=NULLIF($3,''),result_reason=$4,reviewed_by=$5,updated_at=now() WHERE id=$1`, id, decision, role, reason, actor)
	if err != nil {
		return err
	}
	if decision == "approved" {
		if err := grant(ctx, tx, canvasID, userID, role, actor); err != nil {
			return err
		}
	}
	if decision != "cancelled" {
		if err := notify(ctx, tx, userID, "access_"+decision, canvasID, id, actor); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type Notification struct {
	ID         int64      `json:"id"`
	Type       string     `json:"type"`
	CanvasID   string     `json:"canvas_id"`
	CanvasName string     `json:"canvas_name"`
	EntityID   string     `json:"entity_id"`
	ReadAt     *time.Time `json:"read_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (db *DB) ListNotifications(ctx context.Context, userID string, before int64) ([]Notification, int, error) {
	rows, err := db.pool.Query(ctx, `SELECT n.id,n.type,COALESCE(n.canvas_id::text,''),COALESCE(c.name,''),COALESCE(n.entity_id::text,''),n.read_at,n.created_at FROM notifications n LEFT JOIN canvases c ON c.id=n.canvas_id WHERE n.recipient_id=$1 AND ($2::bigint=0 OR n.id<$2) ORDER BY n.id DESC LIMIT 50`, userID, before)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Type, &n.CanvasID, &n.CanvasName, &n.EntityID, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var unread int
	err = db.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE recipient_id=$1 AND read_at IS NULL`, userID).Scan(&unread)
	return items, unread, err
}
func (db *DB) ReadNotifications(ctx context.Context, userID string, id int64) error {
	_, err := db.pool.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE recipient_id=$1 AND read_at IS NULL AND ($2::bigint=0 OR id=$2)`, userID, id)
	return err
}

// Account-bound notification links can accept by invitation ID without exposing its token.
func (db *DB) InvitationHashForUser(ctx context.Context, id, userID string) ([]byte, error) {
	var hash []byte
	err := db.pool.QueryRow(ctx, `SELECT i.token_hash FROM canvas_invitations i JOIN users u ON u.phone_hash=i.phone_hash WHERE i.id=$1 AND u.id=$2`, id, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrInviteIdentity
	}
	return hash, err
}
