-- Menu Board -- Kanban drag-drop (Track S5, desain "PM Board.dc.html").
-- Urutan manual kartu dalam satu kolom status TIDAK PERNAH ada sebelumnya
-- (S4-10 Phase 1 cuma urut created_at) -- desain minta drag-reorder
-- persisten (aw-store.js moveTaskOrder/taskRank). DOUBLE PRECISION supaya
-- sisip antara dua kartu tidak perlu re-index seluruh kolom (fractional
-- indexing) -- kolom+status yang jadi unit pengurutan, BUKAN project.
ALTER TABLE tasks ADD COLUMN position DOUBLE PRECISION NOT NULL DEFAULT 0;

WITH ranked AS (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY project_id, status_id ORDER BY created_at) AS rn
  FROM tasks
)
UPDATE tasks SET position = ranked.rn
FROM ranked
WHERE tasks.id = ranked.id;

CREATE INDEX idx_tasks_status_position ON tasks (status_id, position);
