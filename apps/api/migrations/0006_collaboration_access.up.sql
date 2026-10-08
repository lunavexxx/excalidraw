CREATE TABLE canvas_invitations (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 canvas_id UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
 phone_hash BYTEA NOT NULL,
 phone_cipher BYTEA NOT NULL,
 phone_masked TEXT NOT NULL,
 role TEXT NOT NULL CHECK (role IN ('editor','viewer')),
 invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
 token_hash BYTEA NOT NULL UNIQUE,
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','revoked')),
 expires_at TIMESTAMPTZ NOT NULL,
 accepted_by UUID REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX canvas_invitations_canvas_idx ON canvas_invitations(canvas_id,created_at);
CREATE TABLE canvas_access_entries (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 canvas_id UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
 token_hash BYTEA NOT NULL UNIQUE,
 created_by UUID REFERENCES users(id) ON DELETE SET NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE canvas_access_requests (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 canvas_id UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 role TEXT NOT NULL CHECK (role IN ('editor','viewer')),
 reason TEXT NOT NULL DEFAULT '' CHECK (char_length(reason)<=1000),
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
 reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
 granted_role TEXT CHECK (granted_role IN ('editor','viewer')),
 result_reason TEXT NOT NULL DEFAULT '' CHECK (char_length(result_reason)<=1000),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX canvas_access_requests_pending_idx ON canvas_access_requests(canvas_id,user_id) WHERE status='pending';
CREATE INDEX canvas_access_requests_user_idx ON canvas_access_requests(user_id,created_at);
CREATE TABLE notifications (
 id BIGSERIAL PRIMARY KEY,
 recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 type TEXT NOT NULL,
 canvas_id UUID REFERENCES canvases(id) ON DELETE CASCADE,
 entity_id UUID,
 actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
 read_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_recipient_idx ON notifications(recipient_id,id DESC);
CREATE TABLE collaboration_outbox (
 id BIGSERIAL PRIMARY KEY,
 canvas_id UUID,
 recipient_id UUID,
 type TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- All ACL mutations, including workspace member operations, invalidate active rooms.
CREATE FUNCTION collaboration_acl_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='workspace_members' THEN
  INSERT INTO collaboration_outbox(canvas_id,type)
  SELECT id,'access' FROM canvases WHERE workspace_id=COALESCE(NEW.workspace_id,OLD.workspace_id);
 ELSIF TG_TABLE_NAME='canvases' THEN
  INSERT INTO collaboration_outbox(canvas_id,type) VALUES (COALESCE(NEW.id,OLD.id),'access');
 ELSE
  INSERT INTO collaboration_outbox(canvas_id,type) VALUES (COALESCE(NEW.canvas_id,OLD.canvas_id),'access');
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER collaborators_access_event AFTER INSERT OR UPDATE OR DELETE ON canvas_collaborators FOR EACH ROW EXECUTE FUNCTION collaboration_acl_event();
CREATE TRIGGER workspace_access_event AFTER INSERT OR UPDATE OR DELETE ON workspace_members FOR EACH ROW EXECUTE FUNCTION collaboration_acl_event();
CREATE TRIGGER share_access_event AFTER INSERT OR UPDATE OR DELETE ON share_links FOR EACH ROW EXECUTE FUNCTION collaboration_acl_event();
CREATE TRIGGER canvas_access_event AFTER UPDATE OF deleted_at,workspace_id,owner_id OR DELETE ON canvases FOR EACH ROW EXECUTE FUNCTION collaboration_acl_event();
CREATE FUNCTION collaboration_notification_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO collaboration_outbox(recipient_id,type) VALUES (NEW.recipient_id,'notifications');
 RETURN NULL;
END $$;
CREATE TRIGGER notification_event AFTER INSERT OR UPDATE ON notifications FOR EACH ROW EXECUTE FUNCTION collaboration_notification_event();
