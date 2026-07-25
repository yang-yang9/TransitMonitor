-- Notifier config (encrypted at rest): single row "notifiers" holding JSON of
-- all notifier settings (dingtalk/webhook/lark/slack/qq). Mirrors the
-- credentials table shape (ciphertext + nonce).
CREATE TABLE notifier_config (
  id TEXT PRIMARY KEY,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
