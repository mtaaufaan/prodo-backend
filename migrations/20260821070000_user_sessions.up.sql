-- S1-26: tracking sesi JWT aktif untuk fitur multi-device dan remote logout
-- (US-004/US-005) -- lihat docs/DATABASE_SCHEMA.md §5.3.
CREATE TABLE user_sessions (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  jti              VARCHAR(128) NOT NULL,
  device_info      JSONB,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at       TIMESTAMPTZ NOT NULL,
  revoked_at       TIMESTAMPTZ,
  last_active_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_sessions_jti UNIQUE (jti)
);

CREATE INDEX idx_user_sessions_user_id ON user_sessions (user_id);
CREATE INDEX idx_user_sessions_jti ON user_sessions (jti);
CREATE INDEX idx_user_sessions_active ON user_sessions (user_id, expires_at)
  WHERE revoked_at IS NULL;
