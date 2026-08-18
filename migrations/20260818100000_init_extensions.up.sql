-- Extensions dasar yang dibutuhkan seluruh skema PRODO (lihat
-- docs/DATABASE_SCHEMA.md §1). Dev lokal sudah membuat extension yang sama
-- lewat infra/postgres/init.sql saat container pertama kali dibuat --
-- IF NOT EXISTS membuat migration ini aman dijalankan ulang di sana.
-- Staging/production TIDAK punya init.sql itu, jadi migration inilah
-- satu-satunya sumber kebenaran untuk environment tersebut.
--
-- pgaudit sengaja TIDAK dibuat di sini: butuh shared_preload_libraries
-- di-set di level server (command-line/postgresql.conf), bukan sesuatu
-- yang bisa diselesaikan oleh CREATE EXTENSION saja -- lihat
-- docs/security-compliance.md §5.3.
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "ltree";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
