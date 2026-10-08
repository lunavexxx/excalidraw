DROP TRIGGER IF EXISTS notification_event ON notifications;
DROP FUNCTION IF EXISTS collaboration_notification_event();
DROP TRIGGER IF EXISTS canvas_access_event ON canvases;
DROP TRIGGER IF EXISTS share_access_event ON share_links;
DROP TRIGGER IF EXISTS workspace_access_event ON workspace_members;
DROP TRIGGER IF EXISTS collaborators_access_event ON canvas_collaborators;
DROP FUNCTION IF EXISTS collaboration_acl_event();
DROP TABLE IF EXISTS collaboration_outbox, notifications, canvas_access_requests, canvas_access_entries, canvas_invitations;
