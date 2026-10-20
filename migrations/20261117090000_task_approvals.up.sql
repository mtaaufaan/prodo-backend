-- Keputusan approval task (S7A-04, Sprint 7 "Approver Console", implementation_gaps.md IG-150; US approval fase review).
--
-- Satu baris per KEPUTUSAN (setuju/tolak), append-only: task yang ditolak lalu disetujui kemudian punya dua baris.
-- Sumber riwayat "Sudah Diputuskan" (permanen) pada GET /me/approvals. Perpindahan status itu sendiri tetap
-- lewat jalur status task (sesi status, PIC fase, dependency, rule); tabel ini hanya mencatat keputusannya.
CREATE TABLE task_approvals (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  approver_id UUID NOT NULL REFERENCES users(id),
  decision    VARCHAR(10) NOT NULL CHECK (decision IN ('approved', 'rejected')),
  note        TEXT,
  status_from VARCHAR(100) NOT NULL,
  status_to   VARCHAR(100) NOT NULL,
  decided_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  -- Tolak wajib beralasan (aturan produk); setuju boleh tanpa catatan.
  CONSTRAINT task_approvals_reject_needs_note CHECK (decision <> 'rejected' OR length(btrim(coalesce(note, ''))) >= 5)
);

CREATE INDEX idx_task_approvals_approver ON task_approvals (approver_id, decided_at DESC);
CREATE INDEX idx_task_approvals_task ON task_approvals (task_id);

ALTER TABLE task_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_approvals FORCE ROW LEVEL SECURITY;

-- Baca: siapa pun yang boleh melihat task induknya (pola task_comments). Tulis: hanya sebagai diri sendiri dan hanya
-- pada task yang boleh ditulis; aturan role (Approver/PM/Admin Workspace) ditegakkan di layanan. UPDATE/DELETE tidak
-- dibuka -- riwayat keputusan tidak boleh diubah.
CREATE POLICY task_approvals_select ON task_approvals
  FOR SELECT TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_approvals.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_approvals_insert ON task_approvals
  FOR INSERT TO prodo_app
  WITH CHECK (
    approver_id = prodo_current_user_id()
    AND EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_approvals.task_id
        AND (prodo_is_platform_admin() OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
