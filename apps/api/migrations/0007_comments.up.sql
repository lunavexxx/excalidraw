CREATE TABLE comment_threads (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 canvas_id UUID NOT NULL REFERENCES canvases(id) ON DELETE CASCADE,
 created_by UUID REFERENCES users(id) ON DELETE SET NULL,
 anchor JSONB NOT NULL,
 anchor_element_version BIGINT NOT NULL DEFAULT 0,
 anchor_element_nonce BIGINT NOT NULL DEFAULT 0,
 resolved_at TIMESTAMPTZ,
 resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
 deleted_at TIMESTAMPTZ,
 version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
 unread_version BIGINT NOT NULL DEFAULT 1 CHECK (unread_version>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 activity_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX comment_threads_canvas_idx ON comment_threads(canvas_id,activity_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX comment_threads_element_idx ON comment_threads(canvas_id,(anchor->>'element_id')) WHERE deleted_at IS NULL;
CREATE TABLE comments (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 seq BIGSERIAL UNIQUE,
 thread_id UUID NOT NULL REFERENCES comment_threads(id) ON DELETE CASCADE,
 author_id UUID REFERENCES users(id) ON DELETE SET NULL,
 body TEXT NOT NULL CHECK (char_length(body)<=5000),
 is_root BOOLEAN NOT NULL DEFAULT false,
 request_id UUID NOT NULL,
 request_hash TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 edited_at TIMESTAMPTZ,
 deleted_at TIMESTAMPTZ,
 UNIQUE(author_id,request_id)
);
CREATE UNIQUE INDEX comments_root_idx ON comments(thread_id) WHERE is_root;
CREATE INDEX comments_thread_idx ON comments(thread_id,seq DESC);
CREATE INDEX comments_author_idx ON comments(author_id,created_at DESC);
CREATE TABLE comment_mentions (
 comment_id UUID NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 start_offset INTEGER NOT NULL CHECK (start_offset>=0),
 end_offset INTEGER NOT NULL CHECK (end_offset>start_offset),
 PRIMARY KEY(comment_id,start_offset)
);
CREATE TABLE comment_reactions (
 comment_id UUID NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 emoji TEXT NOT NULL CHECK (octet_length(emoji)<=128),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(comment_id,user_id,emoji)
);
CREATE TABLE comment_thread_reads (
 thread_id UUID NOT NULL REFERENCES comment_threads(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 last_read_version BIGINT NOT NULL CHECK (last_read_version>=0),
 read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(thread_id,user_id)
);
CREATE INDEX comment_thread_reads_user_idx ON comment_thread_reads(user_id,thread_id);
ALTER TABLE notifications ADD COLUMN event_key TEXT;
ALTER TABLE notifications ADD COLUMN source_version BIGINT;
CREATE UNIQUE INDEX notifications_event_idx ON notifications(recipient_id,event_key) WHERE event_key IS NOT NULL;
CREATE INDEX notifications_unread_idx ON notifications(recipient_id,id DESC) WHERE read_at IS NULL;
CREATE INDEX notifications_comment_idx ON notifications(recipient_id,entity_id,source_version) WHERE read_at IS NULL AND source_version IS NOT NULL;
ALTER TABLE collaboration_outbox ADD COLUMN thread_id UUID;
ALTER TABLE collaboration_outbox ADD COLUMN source_version BIGINT;
-- Independent receipts avoid skipping a lower sequence whose transaction commits later.
CREATE TABLE collaboration_consumers (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE collaboration_outbox_receipts (
 consumer_id UUID NOT NULL REFERENCES collaboration_consumers(id) ON DELETE CASCADE,
 event_id BIGINT NOT NULL REFERENCES collaboration_outbox(id) ON DELETE CASCADE,
 PRIMARY KEY(consumer_id,event_id)
);
-- Keep the last authoritative element position even if its tombstone is later
-- removed from a scene. The same version/nonce order is used by scene folding.
CREATE FUNCTION comment_anchor_fallback() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE elements JSONB; e JSONB; ex DOUBLE PRECISION; ey DOUBLE PRECISION;
 ew DOUBLE PRECISION; eh DOUBLE PRECISION; angle DOUBLE PRECISION;
 ev BIGINT; nonce BIGINT;
BEGIN
 IF TG_TABLE_NAME='canvas_events' THEN elements:=NEW.elements;
 ELSE elements:=NEW.data->'elements'; END IF;
 IF jsonb_typeof(elements) IS DISTINCT FROM 'array' THEN RETURN NULL; END IF;
 FOR e IN SELECT value FROM jsonb_array_elements(elements) LOOP
  IF jsonb_typeof(e->'x') IS DISTINCT FROM 'number' OR jsonb_typeof(e->'y') IS DISTINCT FROM 'number'
   OR jsonb_typeof(e->'width') IS DISTINCT FROM 'number' OR jsonb_typeof(e->'height') IS DISTINCT FROM 'number'
   OR jsonb_typeof(e->'version') IS DISTINCT FROM 'number' THEN CONTINUE; END IF;
  BEGIN
  ex:=(e->>'x')::double precision;ey:=(e->>'y')::double precision;
  ew:=(e->>'width')::double precision;eh:=(e->>'height')::double precision;
  angle:=CASE WHEN jsonb_typeof(e->'angle')='number' THEN (e->>'angle')::double precision ELSE 0 END;
  ev:=(e->>'version')::numeric::bigint;
  nonce:=CASE WHEN jsonb_typeof(e->'versionNonce')='number' THEN (e->>'versionNonce')::numeric::bigint ELSE 0 END;
  EXCEPTION WHEN numeric_value_out_of_range OR invalid_text_representation THEN CONTINUE;
  END;
  IF abs(ex)>1e100 OR abs(ey)>1e100 OR abs(ew)>1e100 OR abs(eh)>1e100 OR abs(angle)>1e100 THEN CONTINUE; END IF;
  UPDATE comment_threads t SET anchor=t.anchor || jsonb_build_object(
   'x',ex+ew/2+cos(angle)*((COALESCE((t.anchor->>'u')::double precision,0)-0.5)*ew)-sin(angle)*((COALESCE((t.anchor->>'v')::double precision,0)-0.5)*eh),
   'y',ey+eh/2+sin(angle)*((COALESCE((t.anchor->>'u')::double precision,0)-0.5)*ew)+cos(angle)*((COALESCE((t.anchor->>'v')::double precision,0)-0.5)*eh)),
   anchor_element_version=ev,anchor_element_nonce=nonce
   WHERE t.canvas_id=NEW.canvas_id AND t.deleted_at IS NULL AND t.anchor->>'element_id'=e->>'id'
   AND (ev>t.anchor_element_version OR (ev=t.anchor_element_version AND nonce<t.anchor_element_nonce));
 END LOOP;
 RETURN NULL;
END $$;
CREATE TRIGGER comment_event_anchor AFTER INSERT ON canvas_events FOR EACH ROW EXECUTE FUNCTION comment_anchor_fallback();
CREATE TRIGGER comment_scene_anchor AFTER INSERT OR UPDATE OF data ON canvas_scenes FOR EACH ROW EXECUTE FUNCTION comment_anchor_fallback();
