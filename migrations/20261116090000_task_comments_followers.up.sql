-- Komentar task, follower, dan catatan mention (S6E-04, Sprint 6 "Editor Console",
-- implementation_gaps.md IG-135; US-030/US-031/US-033).
--
-- task_comments mengikuti DATABASE_SCHEMA.md §5.20 (content JSONB TipTap, content_before_edit,
-- is_edited, edit_locked_at) + kolom edited_at (waktu edit terakhir untuk penanda "(diedit)").
-- Isi disimpan sebagai dokumen TipTap minimal; API memakai teks biasa (desain = textarea).
CREATE TABLE task_comments (
  id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id             UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  user_id             UUID NOT NULL REFERENCES users(id),
  content             JSONB NOT NULL,
  content_before_edit JSONB,
  is_edited           BOOLEAN NOT NULL DEFAULT FALSE,
  edited_at           TIMESTAMPTZ,
  edit_locked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '3 minutes',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at          TIMESTAMPTZ
);

CREATE INDEX idx_task_comments_task ON task_comments (task_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_task_comments_user ON task_comments (user_id, created_at);

-- task_followers -- "Diikuti" di Task Saya. source: asal baris (otomatis saat berkomentar,
-- di-mention, atau ditugaskan; atau manual lewat PUT /tasks/:id/follow).
CREATE TABLE task_followers (
  task_id    UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source     VARCHAR(20) NOT NULL CHECK (source IN ('comment', 'mention', 'assignment', 'manual')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (task_id, user_id)
);

CREATE INDEX idx_task_followers_user ON task_followers (user_id);

-- task_comment_mentions -- satu baris per (komentar, user yang di-mention). Dasar:
--  * tidak mengirim ulang notifikasi untuk mention yang sama saat komentar diedit (US-030);
--  * cooldown notifikasi mention (US-033): digest_state 'sent' = notifikasi langsung terkirim,
--    'pending' = diakumulasi (dikirim sebagai ringkasan oleh job setelah window_ends_at),
--    'dropped' = ditekan (digest workspace nonaktif).
CREATE TABLE task_comment_mentions (
  comment_id     UUID NOT NULL REFERENCES task_comments(id) ON DELETE CASCADE,
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  task_id        UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  actor_id       UUID NOT NULL REFERENCES users(id),
  digest_state   VARCHAR(10) NOT NULL CHECK (digest_state IN ('sent', 'pending', 'dropped')),
  notified_at    TIMESTAMPTZ,
  window_ends_at TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (comment_id, user_id)
);

CREATE INDEX idx_task_comment_mentions_pending ON task_comment_mentions (window_ends_at) WHERE digest_state = 'pending';
CREATE INDEX idx_task_comment_mentions_user ON task_comment_mentions (user_id, notified_at);

-- RLS -- siapa pun yang boleh melihat/menulis task induk boleh mengakses baris anaknya (pola
-- task_assignees/task_checklist_items). Aturan penulis/jendela edit/izin hapus ditegakkan di
-- service; DELETE SQL tidak dibuka (komentar soft delete lewat UPDATE deleted_at).
ALTER TABLE task_comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_comments FORCE ROW LEVEL SECURITY;
ALTER TABLE task_followers ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_followers FORCE ROW LEVEL SECURITY;
ALTER TABLE task_comment_mentions ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_comment_mentions FORCE ROW LEVEL SECURITY;

CREATE POLICY task_comments_select ON task_comments
  FOR SELECT TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_comments.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_comments_insert ON task_comments
  FOR INSERT TO prodo_app
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_comments.task_id
        AND (prodo_is_platform_admin() OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_comments_update ON task_comments
  FOR UPDATE TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_comments.task_id
        AND (prodo_is_platform_admin() OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_followers_all ON task_followers
  FOR ALL TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_followers.task_id
        AND (prodo_is_platform_admin() OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_comment_mentions_all ON task_comment_mentions
  FOR ALL TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_comment_mentions.task_id
        AND (prodo_is_platform_admin() OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
