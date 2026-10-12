package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/lunavexxx/excalidraw/apps/api/internal/commentemoji"
)

var commentUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	ErrCommentNotFound  = errors.New("comment unavailable")
	ErrCommentForbidden = errors.New("comment permission required")
	ErrCommentConflict  = errors.New("comment changed; reload and retry")
	ErrCommentInvalid   = errors.New("invalid comment")
	ErrCommentRateLimit = errors.New("too many comments; try again shortly")
)

type CommentAnchor struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	ElementID string  `json:"element_id,omitempty"`
	U         float64 `json:"u"`
	V         float64 `json:"v"`
}

func (a CommentAnchor) Valid() bool {
	return !math.IsNaN(a.X) && !math.IsInf(a.X, 0) && !math.IsNaN(a.Y) && !math.IsInf(a.Y, 0) && math.Abs(a.X) <= 1e9 && math.Abs(a.Y) <= 1e9 && len(a.ElementID) <= 100 && !math.IsNaN(a.U) && !math.IsInf(a.U, 0) && !math.IsNaN(a.V) && !math.IsInf(a.V, 0) && a.U >= 0 && a.U <= 1 && a.V >= 0 && a.V <= 1
}

type CommentAuthor struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}
type CommentMention struct {
	UserID string `json:"user_id"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}
type CommentReaction struct {
	Emoji string          `json:"emoji"`
	Users []CommentAuthor `json:"users"`
}
type Comment struct {
	ID        string            `json:"id"`
	Seq       int64             `json:"seq"`
	Author    CommentAuthor     `json:"author"`
	Body      string            `json:"body"`
	IsRoot    bool              `json:"is_root"`
	Mentions  []CommentMention  `json:"mentions"`
	Reactions []CommentReaction `json:"reactions"`
	CreatedAt time.Time         `json:"created_at"`
	EditedAt  *time.Time        `json:"edited_at"`
	DeletedAt *time.Time        `json:"deleted_at"`
}
type CommentThread struct {
	ID            string        `json:"id"`
	CanvasID      string        `json:"canvas_id"`
	Author        CommentAuthor `json:"author"`
	Anchor        CommentAnchor `json:"anchor"`
	ResolvedAt    *time.Time    `json:"resolved_at"`
	Version       int64         `json:"version"`
	UnreadVersion int64         `json:"unread_version"`
	IsUnread      bool          `json:"is_unread"`
	CreatedAt     time.Time     `json:"created_at"`
	ActivityAt    time.Time     `json:"activity_at"`
	Preview       string        `json:"preview"`
	RootDeleted   bool          `json:"root_deleted"`
	ReplyCount    int           `json:"reply_count"`
	CanResolve    bool          `json:"can_resolve"`
	CanDelete     bool          `json:"can_delete"`
}
type CommentThreadPage struct {
	Items      []CommentThread `json:"items"`
	NextCursor string          `json:"next_cursor"`
	Total      int             `json:"total"`
	Unread     int             `json:"unread"`
}
type CommentThreadDetail struct {
	Thread     CommentThread `json:"thread"`
	Comments   []Comment     `json:"comments"`
	NextBefore int64         `json:"next_before"`
}
type CommentListOptions struct {
	Search          string
	Sort            string
	IncludeResolved bool
	Cursor          string
	Limit           int
}
type commentCursor struct {
	ID   string    `json:"id"`
	At   time.Time `json:"at"`
	Rank int       `json:"rank"`
}

const threadSelect = `SELECT t.id,t.canvas_id,COALESCE(u.id::text,''),COALESCE(u.nickname,''),COALESCE(u.avatar_url,''),t.anchor,t.resolved_at,t.version,t.unread_version,
 (NULLIF($2,'')::uuid IS NOT NULL AND t.unread_version>COALESCE(r.last_read_version,0)),t.created_at,t.activity_at,
 COALESCE((SELECT CASE WHEN c.deleted_at IS NULL THEN c.body ELSE '' END FROM comments c WHERE c.thread_id=t.id AND c.is_root),''),
 COALESCE((SELECT c.deleted_at IS NOT NULL FROM comments c WHERE c.thread_id=t.id AND c.is_root),false),
 (SELECT count(*) FROM comments c WHERE c.thread_id=t.id AND NOT c.is_root)
 FROM comment_threads t LEFT JOIN users u ON u.id=t.created_by
 LEFT JOIN comment_thread_reads r ON r.thread_id=t.id AND r.user_id=NULLIF($2,'')::uuid `

func scanCommentThread(row rowScanner, user string, management bool) (CommentThread, error) {
	var t CommentThread
	var anchor []byte
	err := row.Scan(&t.ID, &t.CanvasID, &t.Author.ID, &t.Author.Name, &t.Author.Avatar, &anchor, &t.ResolvedAt, &t.Version, &t.UnreadVersion, &t.IsUnread, &t.CreatedAt, &t.ActivityAt, &t.Preview, &t.RootDeleted, &t.ReplyCount)
	if err == nil {
		err = json.Unmarshal(anchor, &t.Anchor)
	}
	t.CanResolve = management || (user != "" && user == t.Author.ID)
	t.CanDelete = management
	return t, err
}
func (db *DB) ListCommentThreads(ctx context.Context, canvasID, user string, management bool, opts CommentListOptions) (CommentThreadPage, error) {
	page := CommentThreadPage{Items: []CommentThread{}}
	if opts.Limit < 1 || opts.Limit > 100 || len(opts.Search) > 1000 || len(opts.Cursor) > 512 || (opts.Sort != "date" && opts.Sort != "unread") {
		return page, ErrCommentInvalid
	}
	var cursor commentCursor
	if opts.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(opts.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || !commentUUID.MatchString(cursor.ID) || cursor.At.IsZero() || (cursor.Rank != 0 && cursor.Rank != 1) {
			return page, ErrCommentInvalid
		}
	}
	rank := `CASE WHEN $4='unread' AND NULLIF($2,'')::uuid IS NOT NULL AND t.unread_version>COALESCE(r.last_read_version,0) THEN 1 ELSE 0 END`
	rows, err := db.pool.Query(ctx, threadSelect+` WHERE t.canvas_id=$1 AND t.deleted_at IS NULL AND ($3 OR t.resolved_at IS NULL)
 AND ($5='' OR EXISTS(SELECT 1 FROM comments c LEFT JOIN users a ON a.id=c.author_id WHERE c.thread_id=t.id AND c.deleted_at IS NULL AND (strpos(lower(c.body),lower($5))>0 OR strpos(lower(a.nickname),lower($5))>0)))
 AND ($6='' OR (`+rank+`,t.activity_at,t.id)<($7,$8,NULLIF($6,'')::uuid))
 ORDER BY `+rank+` DESC,t.activity_at DESC,t.id DESC LIMIT $9`, canvasID, user, opts.IncludeResolved, opts.Sort, opts.Search, cursor.ID, cursor.Rank, cursor.At, opts.Limit+1)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		t, err := scanCommentThread(rows, user, management)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Items) > opts.Limit {
		page.Items = page.Items[:opts.Limit]
		last := page.Items[len(page.Items)-1]
		rank := 0
		if opts.Sort == "unread" && last.IsUnread {
			rank = 1
		}
		raw, _ := json.Marshal(commentCursor{last.ID, last.ActivityAt, rank})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	err = db.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE NULLIF($2,'')::uuid IS NOT NULL AND t.unread_version>COALESCE(r.last_read_version,0)) FROM comment_threads t LEFT JOIN comment_thread_reads r ON r.thread_id=t.id AND r.user_id=NULLIF($2,'')::uuid WHERE t.canvas_id=$1 AND t.deleted_at IS NULL AND ($3 OR t.resolved_at IS NULL)`, canvasID, user, opts.IncludeResolved).Scan(&page.Total, &page.Unread)
	return page, err
}
func (db *DB) GetCommentThread(ctx context.Context, canvasID, id, user string, management bool, before int64) (CommentThreadDetail, error) {
	result := CommentThreadDetail{Comments: []Comment{}}
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	result.Thread, err = scanCommentThread(tx.QueryRow(ctx, threadSelect+`WHERE t.canvas_id=$1 AND t.id=$3 AND t.deleted_at IS NULL`, canvasID, user, id), user, management)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrCommentNotFound
	}
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id,c.seq,COALESCE(u.id::text,''),COALESCE(u.nickname,''),COALESCE(u.avatar_url,''),CASE WHEN c.deleted_at IS NULL THEN c.body ELSE '' END,c.is_root,c.created_at,c.edited_at,c.deleted_at
 FROM comments c LEFT JOIN users u ON u.id=c.author_id WHERE c.thread_id=$1 AND (c.is_root OR c.id IN (SELECT id FROM comments WHERE thread_id=$1 AND NOT is_root AND ($2::bigint=0 OR seq<$2) ORDER BY seq DESC LIMIT 51)) ORDER BY c.seq`, id, before)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var c Comment
		c.Mentions = []CommentMention{}
		c.Reactions = []CommentReaction{}
		err = rows.Scan(&c.ID, &c.Seq, &c.Author.ID, &c.Author.Name, &c.Author.Avatar, &c.Body, &c.IsRoot, &c.CreatedAt, &c.EditedAt, &c.DeletedAt)
		if err != nil {
			rows.Close()
			return result, err
		}
		result.Comments = append(result.Comments, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(result.Comments) > 51 {
		result.Comments = append(result.Comments[:1], result.Comments[2:]...)
		result.NextBefore = result.Comments[1].Seq
	}
	ids := []string{}
	byID := map[string]*Comment{}
	for i := range result.Comments {
		c := &result.Comments[i]
		if c.DeletedAt == nil {
			ids = append(ids, c.ID)
			byID[c.ID] = c
		}
	}
	rows, err = tx.Query(ctx, `SELECT comment_id,user_id,start_offset,end_offset FROM comment_mentions WHERE comment_id=ANY($1::uuid[]) ORDER BY start_offset`, ids)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id string
		var m CommentMention
		if err = rows.Scan(&id, &m.UserID, &m.Start, &m.End); err != nil {
			rows.Close()
			return result, err
		}
		byID[id].Mentions = append(byID[id].Mentions, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = tx.Query(ctx, `SELECT r.comment_id,r.emoji,u.id,u.nickname,COALESCE(u.avatar_url,'') FROM comment_reactions r JOIN users u ON u.id=r.user_id WHERE r.comment_id=ANY($1::uuid[]) ORDER BY r.emoji,r.created_at,u.id`, ids)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id, emoji string
		var u CommentAuthor
		if err = rows.Scan(&id, &emoji, &u.ID, &u.Name, &u.Avatar); err != nil {
			rows.Close()
			return result, err
		}
		c := byID[id]
		if len(c.Reactions) == 0 || c.Reactions[len(c.Reactions)-1].Emoji != emoji {
			c.Reactions = append(c.Reactions, CommentReaction{emoji, []CommentAuthor{}})
		}
		last := &c.Reactions[len(c.Reactions)-1]
		last.Users = append(last.Users, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

// Lock the canvas before the thread. Every mutation rechecks the account ACL
// inside the transaction, including viewer access and management capabilities.
func commentAccess(ctx context.Context, tx pgx.Tx, canvasID, user string) (bool, error) {
	if user == "" {
		return false, ErrCommentForbidden
	}
	var owner, ws, cc string
	err := tx.QueryRow(ctx, `SELECT c.owner_id,COALESCE(w.role,''),COALESCE(a.role,'') FROM canvases c LEFT JOIN workspace_members w ON w.workspace_id=c.workspace_id AND w.user_id=$2 LEFT JOIN canvas_collaborators a ON a.canvas_id=c.id AND a.user_id=$2 WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c`, canvasID, user).Scan(&owner, &ws, &cc)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrCommentNotFound
	}
	if err != nil {
		return false, err
	}
	if ResolveCanvasRole(owner, user, ws, cc) == RoleNone {
		return false, ErrCommentNotFound
	}
	return CanManageCanvas(owner, user, ws), nil
}

type lockedCommentThread struct {
	Creator         string
	Resolved        bool
	Version, Unread int64
}

func lockCommentThread(ctx context.Context, tx pgx.Tx, canvasID, id string) (lockedCommentThread, error) {
	var t lockedCommentThread
	err := tx.QueryRow(ctx, `SELECT COALESCE(created_by::text,''),resolved_at IS NOT NULL,version,unread_version FROM comment_threads WHERE canvas_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, canvasID, id).Scan(&t.Creator, &t.Resolved, &t.Version, &t.Unread)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrCommentNotFound
	}
	return t, err
}
func commentEvent(ctx context.Context, tx pgx.Tx, canvasID, id, recipient, kind string, version int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO collaboration_outbox(canvas_id,thread_id,recipient_id,type,source_version) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5)`, canvasID, id, recipient, kind, version)
	return err
}
func markThreadRead(ctx context.Context, tx pgx.Tx, canvasID, id, user string, unread, version int64) error {
	readTag, err := tx.Exec(ctx, `INSERT INTO comment_thread_reads(thread_id,user_id,last_read_version) VALUES($1,$2,$3) ON CONFLICT(thread_id,user_id) DO UPDATE SET last_read_version=GREATEST(comment_thread_reads.last_read_version,EXCLUDED.last_read_version),read_at=now() WHERE comment_thread_reads.last_read_version<EXCLUDED.last_read_version`, id, user, unread)
	if err != nil {
		return err
	}
	notificationTag, err := tx.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE recipient_id=$1 AND canvas_id=$2 AND entity_id=$3 AND source_version<=$4 AND read_at IS NULL`, user, canvasID, id, version)
	if err != nil {
		return err
	}
	if readTag.RowsAffected() == 0 && notificationTag.RowsAffected() == 0 {
		return nil
	}
	return commentEvent(ctx, tx, canvasID, id, user, "comment_reads", unread)
}
func notifyComments(ctx context.Context, tx pgx.Tx, canvasID, id, actor, kind, event string, version int64, mentions []CommentMention, participants bool) error {
	recipients := map[string]bool{}
	for _, m := range mentions {
		recipients[m.UserID] = true
	}
	if participants {
		rows, err := tx.Query(ctx, `SELECT created_by FROM comment_threads WHERE id=$1 AND created_by IS NOT NULL UNION SELECT author_id FROM comments WHERE thread_id=$1 AND author_id IS NOT NULL`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var user string
			if err := rows.Scan(&user); err != nil {
				rows.Close()
				return err
			}
			if !recipients[user] {
				recipients[user] = false
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for user, mentioned := range recipients {
		if user == actor {
			continue
		}
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM canvases c WHERE c.id=$1 AND c.deleted_at IS NULL AND (c.owner_id=$2 OR EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id=c.workspace_id AND user_id=$2) OR EXISTS(SELECT 1 FROM canvas_collaborators WHERE canvas_id=c.id AND user_id=$2)))`, canvasID, user).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		typ := kind
		if mentioned {
			typ = "comment_mentioned"
		}
		_, err = tx.Exec(ctx, `INSERT INTO notifications(recipient_id,type,canvas_id,entity_id,actor_id,event_key,source_version) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(recipient_id,event_key) WHERE event_key IS NOT NULL DO NOTHING`, user, typ, canvasID, id, actor, event, version)
		if err != nil {
			return err
		}
	}
	return nil
}
func validCommentBody(body string, mentions []CommentMention) bool {
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" || utf8.RuneCountInString(body) > 5000 || len(body) > 20000 || len(mentions) > 50 {
		return false
	}
	size := len(utf16.Encode([]rune(body)))
	lastEnd := 0
	for _, m := range mentions {
		if m.Start < lastEnd || m.End <= m.Start || m.End > size || !commentUUID.MatchString(m.UserID) {
			return false
		}
		lastEnd = m.End
	}
	return true
}
func replaceMentions(ctx context.Context, tx pgx.Tx, canvasID, commentID, body string, mentions []CommentMention) error {
	text := utf16.Encode([]rune(body))
	for _, m := range mentions {
		var name string
		err := tx.QueryRow(ctx, `SELECT u.nickname FROM users u JOIN canvases c ON c.id=$1 WHERE u.id=$2 AND (c.owner_id=u.id OR EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id=c.workspace_id AND user_id=u.id) OR EXISTS(SELECT 1 FROM canvas_collaborators WHERE canvas_id=c.id AND user_id=u.id))`, canvasID, m.UserID).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCommentInvalid
		}
		if err != nil {
			return err
		}
		if string(utf16.Decode(text[m.Start:m.End])) != "@"+name {
			return ErrCommentInvalid
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM comment_mentions WHERE comment_id=$1`, commentID); err != nil {
		return err
	}
	for _, m := range mentions {
		if _, err := tx.Exec(ctx, `INSERT INTO comment_mentions(comment_id,user_id,start_offset,end_offset) VALUES($1,$2,$3,$4)`, commentID, m.UserID, m.Start, m.End); err != nil {
			return err
		}
	}
	return nil
}

type CommentInput struct {
	Body      string           `json:"body"`
	Mentions  []CommentMention `json:"mentions"`
	RequestID string           `json:"request_id"`
	Anchor    CommentAnchor    `json:"anchor"`
}

// Seed fallback coordinates from the committed scene and its pending events.
// A pin created or rebound after an element moved must not accept an older
// replay frame merely because its own version counter started at zero.
func seedCommentAnchor(ctx context.Context, tx pgx.Tx, canvasID, threadID string, anchor CommentAnchor) error {
	if anchor.ElementID == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `WITH scene AS (SELECT data,folded_upto FROM canvas_scenes WHERE canvas_id=$1), frames AS (
	 SELECT data->'elements' AS elements FROM scene
	 UNION ALL SELECT elements FROM canvas_events WHERE canvas_id=$1 AND id>COALESCE((SELECT folded_upto FROM scene),0)
	) SELECT e FROM frames CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(elements)='array' THEN elements ELSE '[]'::jsonb END) e WHERE e->>'id'=$2`, canvasID, anchor.ElementID)
	if err != nil {
		return err
	}
	var best struct {
		X, Y, Width, Height   *float64
		Angle                 float64
		Version, VersionNonce int64
	}
	found := false
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var element struct {
			X, Y, Width, Height   *float64
			Angle                 float64
			Version, VersionNonce int64
		}
		if json.Unmarshal(raw, &element) != nil || element.X == nil || element.Y == nil || element.Width == nil || element.Height == nil {
			continue
		}
		if !found || element.Version > best.Version || (element.Version == best.Version && element.VersionNonce < best.VersionNonce) {
			best = element
			found = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	dx, dy := (anchor.U-0.5)*(*best.Width), (anchor.V-0.5)*(*best.Height)
	anchor.X = *best.X + *best.Width/2 + math.Cos(best.Angle)*dx - math.Sin(best.Angle)*dy
	anchor.Y = *best.Y + *best.Height/2 + math.Sin(best.Angle)*dx + math.Cos(best.Angle)*dy
	if !anchor.Valid() {
		return nil
	}
	raw, err := json.Marshal(anchor)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE comment_threads SET anchor=$2,anchor_element_version=$3,anchor_element_nonce=$4 WHERE id=$1`, threadID, raw, best.Version, best.VersionNonce)
	return err
}

func (db *DB) CreateComment(ctx context.Context, canvasID, threadID, user string, input CommentInput) (string, error) {
	if !validCommentBody(input.Body, input.Mentions) || input.RequestID == "" || (threadID == "" && !input.Anchor.Valid()) {
		return "", ErrCommentInvalid
	}
	raw, _ := json.Marshal(struct {
		Thread string
		Input  CommentInput
	}{threadID, input})
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = commentAccess(ctx, tx, canvasID, user); err != nil {
		return "", err
	}
	var existingID, existingCanvas, existingHash string
	err = tx.QueryRow(ctx, `SELECT t.id,t.canvas_id,c.request_hash FROM comments c JOIN comment_threads t ON t.id=c.thread_id WHERE c.author_id=$1 AND c.request_id=$2`, user, input.RequestID).Scan(&existingID, &existingCanvas, &existingHash)
	if err == nil {
		if existingCanvas != canvasID || existingHash != hash {
			return "", ErrCommentConflict
		}
		return existingID, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM comments WHERE author_id=$1 AND created_at>now()-interval '1 minute'`, user).Scan(&recent); err != nil {
		return "", err
	}
	if recent >= 30 {
		return "", ErrCommentRateLimit
	}
	root := threadID == ""
	version, unread := int64(1), int64(1)
	if root {
		anchor, _ := json.Marshal(input.Anchor)
		err = tx.QueryRow(ctx, `INSERT INTO comment_threads(canvas_id,created_by,anchor) VALUES($1,$2,$3) RETURNING id`, canvasID, user, anchor).Scan(&threadID)
	} else {
		var t lockedCommentThread
		t, err = lockCommentThread(ctx, tx, canvasID, threadID)
		if err == nil && t.Resolved {
			return "", ErrCommentConflict
		}
		if err == nil {
			err = tx.QueryRow(ctx, `UPDATE comment_threads SET version=version+1,unread_version=unread_version+1,updated_at=now(),activity_at=now() WHERE id=$1 RETURNING version,unread_version`, threadID).Scan(&version, &unread)
		}
	}
	if err != nil {
		return "", err
	}
	if root {
		if err = seedCommentAnchor(ctx, tx, canvasID, threadID, input.Anchor); err != nil {
			return "", err
		}
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO comments(thread_id,author_id,body,is_root,request_id,request_hash) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, threadID, user, input.Body, root, input.RequestID, hash).Scan(&id)
	if err != nil {
		return "", err
	}
	if err = replaceMentions(ctx, tx, canvasID, id, input.Body, input.Mentions); err != nil {
		return "", err
	}
	if err = notifyComments(ctx, tx, canvasID, threadID, user, "comment_replied", "comment:"+id, version, input.Mentions, !root); err != nil {
		return "", err
	}
	if err = markThreadRead(ctx, tx, canvasID, threadID, user, unread, version); err != nil {
		return "", err
	}
	if err = commentEvent(ctx, tx, canvasID, threadID, "", "comments", version); err != nil {
		return "", err
	}
	return threadID, tx.Commit(ctx)
}

type CommentThreadUpdate struct {
	Version  int64          `json:"version"`
	Resolved *bool          `json:"resolved,omitempty"`
	Anchor   *CommentAnchor `json:"anchor,omitempty"`
}

func (db *DB) UpdateCommentThread(ctx context.Context, canvasID, id, user string, input CommentThreadUpdate, remove bool) error {
	if input.Version < 1 || (input.Anchor != nil && !input.Anchor.Valid()) || (!remove && ((input.Resolved == nil) == (input.Anchor == nil))) {
		return ErrCommentInvalid
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	manage, err := commentAccess(ctx, tx, canvasID, user)
	if err != nil {
		return err
	}
	t, err := lockCommentThread(ctx, tx, canvasID, id)
	if err != nil {
		return err
	}
	if (remove && !manage) || (!remove && !manage && t.Creator != user) {
		return ErrCommentForbidden
	}
	if t.Version != input.Version {
		return ErrCommentConflict
	}
	version, unread := t.Version+1, t.Unread
	if remove {
		_, err = tx.Exec(ctx, `UPDATE comment_threads SET deleted_at=now(),version=$2,updated_at=now() WHERE id=$1`, id, version)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE canvas_id=$1 AND entity_id=$2 AND source_version IS NOT NULL AND read_at IS NULL`, canvasID, id)
		}
	} else if input.Resolved != nil {
		if *input.Resolved == t.Resolved {
			return tx.Commit(ctx)
		}
		kind := "comment_resolved"
		if !*input.Resolved {
			kind = "comment_reopened"
			unread++
		}
		_, err = tx.Exec(ctx, `UPDATE comment_threads SET resolved_at=CASE WHEN $2 THEN now() ELSE NULL END,resolved_by=CASE WHEN $2 THEN $3::uuid ELSE NULL END,version=$4,unread_version=$5,updated_at=now(),activity_at=now() WHERE id=$1`, id, *input.Resolved, user, version, unread)
		if err == nil {
			err = notifyComments(ctx, tx, canvasID, id, user, kind, "thread:"+id+":"+strconv.FormatInt(version, 10), version, nil, true)
		}
	} else {
		anchor, _ := json.Marshal(input.Anchor)
		_, err = tx.Exec(ctx, `UPDATE comment_threads SET anchor=$2,anchor_element_version=0,anchor_element_nonce=0,version=$3,updated_at=now() WHERE id=$1`, id, anchor, version)
		if err == nil {
			err = seedCommentAnchor(ctx, tx, canvasID, id, *input.Anchor)
		}
	}
	if err != nil {
		return err
	}
	if !remove {
		if err = markThreadRead(ctx, tx, canvasID, id, user, unread, version); err != nil {
			return err
		}
	}
	if err = commentEvent(ctx, tx, canvasID, id, "", "comments", version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (db *DB) UpdateComment(ctx context.Context, canvasID, threadID, id, user, body string, mentions []CommentMention, version int64, remove bool) error {
	if version < 1 || (!remove && !validCommentBody(body, mentions)) {
		return ErrCommentInvalid
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	manage, err := commentAccess(ctx, tx, canvasID, user)
	if err != nil {
		return err
	}
	t, err := lockCommentThread(ctx, tx, canvasID, threadID)
	if err != nil {
		return err
	}
	var author string
	var deleted bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(author_id::text,''),deleted_at IS NOT NULL FROM comments WHERE id=$1 AND thread_id=$2 FOR UPDATE`, id, threadID).Scan(&author, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCommentNotFound
	}
	if err != nil {
		return err
	}
	if author != user && !(remove && manage) {
		return ErrCommentForbidden
	}
	if deleted {
		if remove {
			return tx.Commit(ctx)
		}
		return ErrCommentNotFound
	}
	if t.Version != version {
		return ErrCommentConflict
	}
	newMentions := []CommentMention{}
	if remove {
		_, err = tx.Exec(ctx, `UPDATE comments SET deleted_at=now(),body='' WHERE id=$1`, id)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM comment_mentions WHERE comment_id=$1`, id)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM comment_reactions WHERE comment_id=$1`, id)
		}
	} else {
		rows, e := tx.Query(ctx, `SELECT DISTINCT user_id::text FROM comment_mentions WHERE comment_id=$1`, id)
		if e != nil {
			return e
		}
		old := map[string]bool{}
		for rows.Next() {
			var u string
			if e = rows.Scan(&u); e != nil {
				rows.Close()
				return e
			}
			old[u] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, m := range mentions {
			if !old[m.UserID] {
				newMentions = append(newMentions, m)
			}
		}
		_, err = tx.Exec(ctx, `UPDATE comments SET body=$2,edited_at=now() WHERE id=$1`, id, body)
		if err == nil {
			err = replaceMentions(ctx, tx, canvasID, id, body, mentions)
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE comment_threads SET version=version+1,updated_at=now() WHERE id=$1`, threadID)
	if err != nil {
		return err
	}
	if err = notifyComments(ctx, tx, canvasID, threadID, user, "comment_mentioned", "edit:"+id+":"+strconv.FormatInt(version+1, 10), version+1, newMentions, false); err != nil {
		return err
	}
	if err = commentEvent(ctx, tx, canvasID, threadID, "", "comments", version+1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (db *DB) SetCommentReaction(ctx context.Context, canvasID, threadID, id, user, emoji string, remove bool) error {
	emoji, ok := commentemoji.Normalize(emoji)
	if !ok {
		return ErrCommentInvalid
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = commentAccess(ctx, tx, canvasID, user); err != nil {
		return err
	}
	t, err := lockCommentThread(ctx, tx, canvasID, threadID)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM comments WHERE id=$1 AND thread_id=$2 AND deleted_at IS NULL)`, id, threadID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrCommentNotFound
	}
	var changed int64
	if remove {
		tag, e := tx.Exec(ctx, `DELETE FROM comment_reactions WHERE comment_id=$1 AND user_id=$2 AND emoji=$3`, id, user, emoji)
		err = e
		changed = tag.RowsAffected()
	} else {
		var count int
		var existing bool
		err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(emoji=$3),false) FROM comment_reactions WHERE comment_id=$1 AND user_id=$2`, id, user, emoji).Scan(&count, &existing)
		if err != nil {
			return err
		}
		if existing {
			return tx.Commit(ctx)
		}
		if count >= 20 {
			return ErrCommentRateLimit
		}
		tag, e := tx.Exec(ctx, `INSERT INTO comment_reactions(comment_id,user_id,emoji) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, user, emoji)
		err = e
		changed = tag.RowsAffected()
	}
	if err != nil {
		return err
	}
	if changed == 0 {
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE comment_threads SET version=version+1,updated_at=now() WHERE id=$1`, threadID)
	if err != nil {
		return err
	}
	if err = commentEvent(ctx, tx, canvasID, threadID, "", "comments", t.Version+1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (db *DB) ReadCommentThread(ctx context.Context, canvasID, id, user string, unread, version int64) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = commentAccess(ctx, tx, canvasID, user); err != nil {
		return err
	}
	t, err := lockCommentThread(ctx, tx, canvasID, id)
	if err != nil {
		return err
	}
	if unread < 0 || version < 1 || unread > t.Unread || version > t.Version {
		return ErrCommentInvalid
	}
	if err = markThreadRead(ctx, tx, canvasID, id, user, unread, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (db *DB) ReadAllCommentThreads(ctx context.Context, canvasID, user string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = commentAccess(ctx, tx, canvasID, user); err != nil {
		return err
	}
	// Canvas locking serializes replies, so the captured versions are exactly the
	// committed state at this operation. Notifications remain scoped to this canvas.
	rows, err := tx.Query(ctx, `SELECT id,unread_version,version FROM comment_threads WHERE canvas_id=$1 AND deleted_at IS NULL`, canvasID)
	if err != nil {
		return err
	}
	type receipt struct {
		ID              string
		Unread, Version int64
	}
	items := []receipt{}
	for rows.Next() {
		var r receipt
		if err = rows.Scan(&r.ID, &r.Unread, &r.Version); err != nil {
			rows.Close()
			return err
		}
		items = append(items, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, r := range items {
		if err = markThreadRead(ctx, tx, canvasID, r.ID, user, r.Unread, r.Version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
