-- tasks.start_date -- diminta user (groundwork Gantt Chart, S7-11/12,
-- H22-24): perkiraan tanggal mulai, dipasangkan dengan due_date (perkiraan
-- selesai) dan estimated_hours (dalam JAM) -- ketiganya saling mengisi di
-- FE (AddTaskModal/TaskDetailModal): isi 2 dari 3 -> yang ketiga (kalau
-- masih kosong) otomatis dihitung. CHECK constraint pola sama sprints
-- (start_date/end_date, §5.14) -- due_date tidak boleh sebelum start_date.
ALTER TABLE tasks ADD COLUMN start_date DATE;

ALTER TABLE tasks ADD CONSTRAINT tasks_start_before_due
  CHECK (due_date IS NULL OR start_date IS NULL OR due_date >= start_date);
