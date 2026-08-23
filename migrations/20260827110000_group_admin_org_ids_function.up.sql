-- S3-38 (implementation_gaps.md IG-14 lanjutan): AccountRepository.
-- ListOrgIDsForGroupAdmin (dipanggil AuthService.syncKeycloakClaims SETIAP
-- login, SEBELUM ada transaksi request-scoped/session variable RLS apapun)
-- JOIN ke tabel `organizations` lewat pool `prodo_app_user` polos --
-- begitu organizations kena FORCE ROW LEVEL SECURITY (S3-42), query ini
-- diam-diam kena filter RLS (app.current_user_id belum pernah diisi di
-- titik BOOTSTRAP ini -- ayam-telur: butuh tahu org GA untuk isi klaim,
-- tapi RLS organizations butuh klaim itu dulu) dan SELALU mengembalikan
-- 0 baris, bukan error -- prodo_org_ids jadi kosong lagi untuk SEMUA GA,
-- persis regresi IG-14 tapi dari arah berbeda. Ketahuan lewat live-test
-- S3-40/41 (GA dengan 2+ org, refresh login, prodo_org_ids ternyata
-- kosong).
--
-- Fix: function SECURITY DEFINER (bypass RLS, pola sama prodo_is_group_
-- admin_of_org) yang dipanggil Go lewat SELECT biasa, bukan JOIN langsung
-- ke organizations dari kode aplikasi.
CREATE OR REPLACE FUNCTION prodo_group_admin_org_ids(p_user_id UUID)
RETURNS SETOF UUID LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT o.id
  FROM group_admin_assignments gaa
  JOIN organizations o ON o.group_id = gaa.group_id
  WHERE gaa.user_id = p_user_id
$$;
