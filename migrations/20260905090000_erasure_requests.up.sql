-- S4P-28, US-060: antrian permintaan Right to Erasure (UU PDP Pasal 43).
-- Tidak di-RLS -- pola sama seperti `groups`/`user_sessions`
-- (docs/RLS_DESIGN.md §8): akses dikontrol RBAC middleware
-- (RequirePlatformAdmin untuk GET/execute/reject) + handler-level WHERE
-- untuk POST (self-request atau AW/PM workspace bersama, lihat
-- service.CreateErasureRequest).
CREATE TYPE erasure_request_status AS ENUM ('PENDING', 'DONE', 'REJECTED');

CREATE TABLE erasure_requests (
  id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id        UUID NOT NULL REFERENCES users(id),
  -- subjek data yang dimintakan penghapusan.
  org_id         UUID NOT NULL REFERENCES organizations(id),
  -- konteks organisasi saat permintaan diajukan (ditampilkan di antrian PA).
  requested_by   UUID NOT NULL REFERENCES users(id),
  -- bisa sama dengan user_id (self-request) atau AW/PM/GA/PA yang mengajukan
  -- atas nama subjek (docs/security-compliance.md §6.2).
  reason         TEXT,
  status         erasure_request_status NOT NULL DEFAULT 'PENDING',
  requested_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  processed_at   TIMESTAMPTZ,
  processed_by   UUID REFERENCES users(id),
  -- Platform Admin yang mengeksekusi atau menolak.
  CONSTRAINT ck_erasure_processed_consistency CHECK (
    (status = 'PENDING' AND processed_at IS NULL AND processed_by IS NULL)
    OR (status IN ('DONE', 'REJECTED') AND processed_at IS NOT NULL AND processed_by IS NOT NULL)
  )
);

CREATE INDEX idx_erasure_requests_status ON erasure_requests (status, requested_at DESC);
CREATE INDEX idx_erasure_requests_user ON erasure_requests (user_id);
