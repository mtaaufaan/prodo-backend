-- Webhook (S4G-19/20/21/22, Track S4G, desain "GA Webhook.dc.html" +
-- "GA Add Webhook.dc.html"). DATABASE_SCHEMA.md §5.24/5.25 mendokumentasikan
-- webhook_configs dengan org_id NOT NULL + enum webhook_event_type -- dua-
-- duanya diperbaiki di sini (lihat implementation_gaps.md IG-44):
--   1. Desain "GA Add Webhook" punya opsi cakupan "Seluruh grup" (bukan cuma
--      per-organisasi) -- org_id jadi NULLABLE (NULL = seluruh grup),
--      group_id ditambah sebagai kolom wajib (pola sama csv_imports).
--   2. events dibatasi TEXT[] + CHECK, bukan enum khusus -- cuma 3 dari 10
--      event di desain yang punya trigger nyata sekarang (project.created/
--      updated/deleted; task.*/comment.created/rule.executed tidak punya
--      tabel/service sama sekali, sama pola gap "kind=task" di csv_imports).
--      CHECK dibatasi ke yang didukung sekarang -- diperluas via migrasi
--      baru begitu Task Management Core/Rule Engine/Comment ada.
CREATE TABLE webhook_configs (
  id                     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  group_id               UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  org_id                 UUID REFERENCES organizations(id) ON DELETE CASCADE,
  name                   VARCHAR(255) NOT NULL,
  target_url             TEXT NOT NULL,
  hmac_secret_encrypted  TEXT NOT NULL,
  events                 TEXT[] NOT NULL,
  is_active              BOOLEAN NOT NULL DEFAULT TRUE,
  created_by             UUID NOT NULL REFERENCES users(id),
  created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE webhook_configs ADD CONSTRAINT chk_webhook_configs_events
  CHECK (events <@ ARRAY['project.created','project.updated','project.deleted']::text[] AND array_length(events, 1) > 0);

CREATE INDEX idx_webhook_configs_group_id ON webhook_configs (group_id);

ALTER TABLE webhook_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_configs FORCE ROW LEVEL SECURITY;

CREATE POLICY webhook_configs_select ON webhook_configs
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

CREATE POLICY webhook_configs_insert ON webhook_configs
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

CREATE POLICY webhook_configs_update ON webhook_configs
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

CREATE POLICY webhook_configs_delete ON webhook_configs
  FOR DELETE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

-- webhook_deliveries: satu baris PER PERCOBAAN (bukan diupdate di tempat) --
-- log pengiriman di desain menampilkan tiap percobaan sebagai baris
-- terpisah ("PERCOBAAN 1/2/3 · RETRY HABIS"), sejalan dengan pola audit
-- trail "immutable snapshot" di codebase ini. Ditulis dari 2 jalur: job
-- Asynq (retry asli, bypass platform_admin) DAN handler HTTP KIRIM TES
-- (sinkron, actor GA asli) -- keduanya perlu INSERT.
CREATE TABLE webhook_deliveries (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  webhook_id      UUID NOT NULL REFERENCES webhook_configs(id) ON DELETE CASCADE,
  event_type      VARCHAR(50) NOT NULL,
  payload         JSONB NOT NULL,
  attempt_number  INTEGER NOT NULL DEFAULT 1,
  status          VARCHAR(12) NOT NULL,
  http_status     INTEGER,
  response_body   TEXT,
  error_message   TEXT,
  duration_ms     INTEGER,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE webhook_deliveries ADD CONSTRAINT chk_webhook_deliveries_status CHECK (status IN ('delivered', 'failed'));

CREATE INDEX idx_webhook_deliveries_webhook_id ON webhook_deliveries (webhook_id, created_at DESC);

ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries FORCE ROW LEVEL SECURITY;

CREATE POLICY webhook_deliveries_select ON webhook_deliveries
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM webhook_configs wc WHERE wc.id = webhook_deliveries.webhook_id AND prodo_is_group_admin_of_group(wc.group_id)
  ));

CREATE POLICY webhook_deliveries_insert ON webhook_deliveries
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM webhook_configs wc WHERE wc.id = webhook_deliveries.webhook_id AND prodo_is_group_admin_of_group(wc.group_id)
  ));
