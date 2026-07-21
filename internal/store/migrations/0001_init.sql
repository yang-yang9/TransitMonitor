-- 0001_init.sql — TransitMonitor initial schema.
-- observed_at columns store Unix-epoch SECONDS (INTEGER) for robust round-trip
-- and SQLite strftime('unixepoch') aggregation.

CREATE TABLE stations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  base_url TEXT NOT NULL,
  config_yaml TEXT NOT NULL,
  poll_interval_seconds INTEGER NOT NULL DEFAULT 300,
  tags TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Credentials: AES-GCM ciphertext + nonce only; NO plaintext columns.
CREATE TABLE credentials (
  station_id TEXT PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE ratio_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  station_id TEXT NOT NULL,
  group_name TEXT NOT NULL,
  model_name TEXT NOT NULL,
  native_ratio REAL,
  native_ratio_kind TEXT,
  quota_type INTEGER,
  input_usd_per_1m REAL,
  output_usd_per_1m REAL,
  cache_read_usd_per_1m REAL,
  cache_write_usd_per_1m REAL,
  fixed_price_usd REAL,
  completion_ratio REAL,
  peak_info TEXT,
  declared_unavailable INTEGER,
  sentinel TEXT,
  note TEXT,
  observed_at INTEGER NOT NULL,
  source_endpoint TEXT
);
CREATE INDEX idx_ratio_station_model_time ON ratio_observations(station_id, model_name, observed_at DESC);
CREATE INDEX idx_ratio_time ON ratio_observations(observed_at DESC);

-- Downsampled hourly aggregates (avg/min/max of comparable USD/1M fields).
CREATE TABLE ratio_observations_hourly (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  station_id TEXT NOT NULL,
  group_name TEXT NOT NULL,
  model_name TEXT NOT NULL,
  hour TEXT NOT NULL,
  avg_input REAL, min_input REAL, max_input REAL,
  avg_output REAL, min_output REAL, max_output REAL,
  UNIQUE(station_id, group_name, model_name, hour)
);

CREATE TABLE snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  station_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  endpoints TEXT,
  payloads BLOB,
  capabilities TEXT
);
CREATE INDEX idx_snap_station_time ON snapshots(station_id, observed_at DESC);

CREATE TABLE change_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  station_id TEXT,
  group_name TEXT,
  model_name TEXT,
  field TEXT,
  old_value TEXT,
  new_value TEXT,
  delta_abs REAL,
  delta_pct REAL,
  observed_at INTEGER NOT NULL,
  severity TEXT
);
CREATE INDEX idx_change_time ON change_events(observed_at DESC);

CREATE TABLE probe_results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  station_id TEXT,
  model_name TEXT,
  tokens_in INTEGER,
  tokens_out INTEGER,
  declared_native_ratio REAL,
  declared_effective_usd_per_1m REAL,
  measured_usd_per_1m REAL,
  markup_pct REAL,
  cost_usd REAL,
  declared_unavailable INTEGER,
  observed_at INTEGER NOT NULL,
  error TEXT
);
CREATE INDEX idx_probe_station_time ON probe_results(station_id, observed_at DESC);

CREATE TABLE alert_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT,
  type TEXT,
  params TEXT,
  enabled INTEGER DEFAULT 1
);

CREATE TABLE alert_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id INTEGER,
  station_id TEXT,
  model_name TEXT,
  payload TEXT,
  sent INTEGER DEFAULT 0,
  error TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts DATETIME DEFAULT CURRENT_TIMESTAMP,
  actor TEXT,
  action TEXT,
  target TEXT,
  detail TEXT
);
