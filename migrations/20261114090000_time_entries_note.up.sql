-- Kolom note (deskripsi opsional entri manual, US-037 AC) dipakai TimeEntryRepository
-- (CreateManual/Get/ListForTask/UpdateManual) sejak IG-97 tetapi tidak pernah ada di
-- migrasi timesheet -- seluruh baca/tulis time_entries gagal "column note does not
-- exist". Ditemukan saat verifikasi DB menu Timesheet & Analitik (IG-128).
ALTER TABLE time_entries ADD COLUMN IF NOT EXISTS note TEXT;
