-- 0003_balance_observations.sql — per-poll balance/quota time series.
-- One row per (station, poll) where a balance source succeeded. Raw fields
-- carry the upstream-native units (new-api internal quota units; sub2api USD);
-- the *_usd fields are the normalized cross-station-comparable values.
-- observed_at is Unix-epoch SECONDS (INTEGER), mirroring ratio_observations.
CREATE TABLE balance_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  station_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  remaining REAL NOT NULL,
  used REAL NOT NULL,
  total REAL NOT NULL,
  remaining_usd REAL NOT NULL,
  used_usd REAL NOT NULL,
  total_usd REAL NOT NULL,
  unlimited INTEGER NOT NULL DEFAULT 0,
  currency TEXT,
  quota_per_unit REAL,
  usd_exchange_rate REAL,
  source_endpoint TEXT
);
CREATE INDEX idx_balance_station_time ON balance_observations(station_id, observed_at DESC);
