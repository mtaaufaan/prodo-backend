-- S16-12 (forward-pull, Track S4G Members & Roles): tambah role
-- 'division_viewer' ke enum workspace_role. File terpisah dari migrasi
-- enum executive (20260915090000) -- sama alasan, ADD VALUE tidak
-- transaksional, tiap enum dapat migration file sendiri.
ALTER TYPE workspace_role ADD VALUE 'division_viewer';
