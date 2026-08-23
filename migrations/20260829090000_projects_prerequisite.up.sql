-- Prasyarat teknis S3-19/21/22/23/25/26/27 (bukan S4-01 sendiri) -- sama pola
-- forward-pull 20260822090000_org_hierarchy_prerequisite (S2-01): S3-19
-- (project_members) dijadwalkan H8 tapi FK ke projects.id, dan projects
-- sendiri baru dijadwalkan S4-01 (sprint BERIKUTNYA) -- dependency FK
-- terbalik, sama root cause. Ditemukan & didiskusikan saat audit H9
-- (implementation_gaps.md IG-17), dikonfirmasi user untuk dimajukan
-- SEKARANG (bukan ditunda ke S4) supaya seluruh sisa US-009b (S3-14 bagian
-- kedua/16/17/19/21/22/23/24/25/26/27) bisa selesai di S3, bukan sekadar
-- didiamkan sampai S4-01.
--
-- Kolom PERSIS DATABASE_SCHEMA.md §5.12/5.13 -- bukan reka-ulang minimal
-- seperti org_hierarchy_prerequisite dulu (skema §5.12/5.13 memang sudah
-- final, tidak ada field S4-01-only yang perlu ditunda).
CREATE TYPE project_scoped_role AS ENUM ('editor', 'approver', 'viewer');

CREATE TABLE projects (
  id                       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  workspace_id             UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name                     VARCHAR(255) NOT NULL,
  description              TEXT,
  accent_color             VARCHAR(50),
  pixel_icon               VARCHAR(50),
  mention_cooldown_minutes SMALLINT,
  is_archived              BOOLEAN NOT NULL DEFAULT FALSE,
  created_by               UUID REFERENCES users(id),
  created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at              TIMESTAMPTZ,
  CONSTRAINT ck_proj_cooldown CHECK (
    mention_cooldown_minutes IS NULL OR mention_cooldown_minutes BETWEEN 10 AND 30
  )
);

CREATE INDEX idx_projects_workspace_id ON projects (workspace_id);

CREATE TABLE project_members (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       project_scoped_role NOT NULL,
  is_scoped  BOOLEAN NOT NULL DEFAULT FALSE,
  added_by   UUID REFERENCES users(id),
  added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (project_id, user_id)
);

CREATE INDEX idx_project_members_user_id ON project_members (user_id);
