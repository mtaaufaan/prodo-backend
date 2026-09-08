-- S16-06 (forward-pull, Track S4G Members & Roles): tambah role 'executive'
-- ke enum user_platform_role. ADD VALUE tidak transaksional -- migrasi ini
-- SENGAJA cuma berisi satu statement, tidak digabung dengan pemakaian nilai
-- barunya di file yang sama (nilai enum baru baru bisa dipakai di transaksi
-- LAIN setelah migrasi ini commit).
ALTER TYPE user_platform_role ADD VALUE 'executive';
