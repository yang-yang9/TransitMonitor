// Package store is the embedded SQLite persistence layer (modernc.org/sqlite,
// pure Go, no CGO). It owns schema migrations, ratio time-series CRUD,
// encrypted credentials, and retention/downsampling. Spec:
// openspec/.../specs/storage-retention/spec.md and station-management (encryption).
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/secrets"

	_ "modernc.org/sqlite"

	"gopkg.in/yaml.v3"
)

// ErrEncryptionDisabled is returned by notifier-config methods when called
// without an encryption key (TRANSMONITOR_ENCRYPTION_KEY unset). Callers fall
// back to YAML config in that case.
var ErrEncryptionDisabled = errors.New("encryption disabled: set TRANSMONITOR_ENCRYPTION_KEY to persist notifier secrets")

//go:embed migrations/*.sql
var migrationsFS embed.FS

// placeholders returns "(?,?,...,?)" with n placeholders.
func placeholders(n int) string {
	if n <= 0 {
		return "()"
	}
	return "(" + strings.Repeat("?,", n-1) + "?)"
}

var ratioCols = []string{
	"station_id", "group_name", "model_name", "native_ratio", "native_ratio_kind", "quota_type",
	"input_usd_per_1m", "output_usd_per_1m", "cache_read_usd_per_1m", "cache_write_usd_per_1m",
	"fixed_price_usd", "completion_ratio", "peak_info", "declared_unavailable", "sentinel", "note",
	"observed_at", "source_endpoint",
}

var changeCols = []string{
	"station_id", "group_name", "model_name", "field", "old_value", "new_value",
	"delta_abs", "delta_pct", "observed_at", "severity",
}

var probeCols = []string{
	"station_id", "model_name", "tokens_in", "tokens_out",
	"declared_native_ratio", "declared_effective_usd_per_1m",
	"measured_usd_per_1m", "markup_pct", "cost_usd",
	"declared_unavailable", "observed_at", "error",
}

var balanceCols = []string{
	"station_id", "observed_at", "remaining", "used", "total",
	"remaining_usd", "used_usd", "total_usd", "unlimited",
	"currency", "quota_per_unit", "usd_exchange_rate", "source_endpoint",
}

// Store wraps a SQLite connection.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.Migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies embedded SQL migrations in order, idempotently.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_version").Scan(&applied); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		ver, err := strconv.Atoi(name[:4])
		if err != nil {
			return fmt.Errorf("bad migration name %s: %w", name, err)
		}
		if ver <= applied {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := s.db.ExecContext(ctx, "INSERT INTO schema_version(version) VALUES(?)", ver); err != nil {
			return err
		}
	}
	return nil
}

// InsertRatioObservations stores a batch of observations (one scrape's worth).
func (s *Store) InsertRatioObservations(ctx context.Context, obs []domain.RatioObservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	q := "INSERT INTO ratio_observations (" + strings.Join(ratioCols, ", ") + ") VALUES " + placeholders(len(ratioCols))
	for _, o := range obs {
		du := 0
		if o.DeclaredUnavailable {
			du = 1
		}
		if _, err := tx.ExecContext(ctx, q,
			o.StationID, o.GroupName, o.ModelName, o.NativeRatio, o.NativeRatioKind, o.QuotaType,
			o.InputUSDPer1M, o.OutputUSDPer1M, o.CacheReadUSDPer1M, o.CacheWriteUSDPer1M,
			o.FixedPriceUSD, o.CompletionRatio, o.PeakInfo, du, o.Sentinel, o.Note,
			o.ObservedAt.Unix(), o.SourceEndpoint,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestRatioObservations returns the most recent observation per (group, model)
// for a station.
func (s *Store) LatestRatioObservations(ctx context.Context, stationID string) ([]domain.RatioObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		station_id, group_name, model_name, native_ratio, native_ratio_kind, quota_type,
		input_usd_per_1m, output_usd_per_1m, cache_read_usd_per_1m, cache_write_usd_per_1m,
		fixed_price_usd, completion_ratio, peak_info, declared_unavailable, sentinel, note,
		observed_at, source_endpoint
		FROM ratio_observations WHERE station_id=? ORDER BY observed_at DESC`, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[string]bool)
	var out []domain.RatioObservation
	for rows.Next() {
		var o domain.RatioObservation
		var du int
		var ts int64
		if err := rows.Scan(
			&o.StationID, &o.GroupName, &o.ModelName, &o.NativeRatio, &o.NativeRatioKind, &o.QuotaType,
			&o.InputUSDPer1M, &o.OutputUSDPer1M, &o.CacheReadUSDPer1M, &o.CacheWriteUSDPer1M,
			&o.FixedPriceUSD, &o.CompletionRatio, &o.PeakInfo, &du, &o.Sentinel, &o.Note,
			&ts, &o.SourceEndpoint,
		); err != nil {
			return nil, err
		}
		o.DeclaredUnavailable = du == 1
		o.ObservedAt = time.Unix(ts, 0).Local()
		key := o.GroupName + "|" + o.ModelName
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	return out, rows.Err()
}

// PrevPollObservations returns the observations from the most recent single poll
// (i.e. all rows sharing the latest observed_at timestamp). Unlike
// LatestRatioObservations (which scans the full history and returns one row per
// group+model ever seen), this returns exactly what the previous poll wrote —
// so models that were already removed won't appear in the result set.
func (s *Store) PrevPollObservations(ctx context.Context, stationID string) ([]domain.RatioObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		station_id, group_name, model_name, native_ratio, native_ratio_kind, quota_type,
		input_usd_per_1m, output_usd_per_1m, cache_read_usd_per_1m, cache_write_usd_per_1m,
		fixed_price_usd, completion_ratio, peak_info, declared_unavailable, sentinel, note,
		observed_at, source_endpoint
		FROM ratio_observations
		WHERE station_id=? AND observed_at = (
			SELECT MAX(observed_at) FROM ratio_observations WHERE station_id=?
		)`, stationID, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RatioObservation
	for rows.Next() {
		var o domain.RatioObservation
		var du int
		var ts int64
		if err := rows.Scan(
			&o.StationID, &o.GroupName, &o.ModelName, &o.NativeRatio, &o.NativeRatioKind, &o.QuotaType,
			&o.InputUSDPer1M, &o.OutputUSDPer1M, &o.CacheReadUSDPer1M, &o.CacheWriteUSDPer1M,
			&o.FixedPriceUSD, &o.CompletionRatio, &o.PeakInfo, &du, &o.Sentinel, &o.Note,
			&ts, &o.SourceEndpoint,
		); err != nil {
			return nil, err
		}
		o.DeclaredUnavailable = du == 1
		o.ObservedAt = time.Unix(ts, 0).Local()
		out = append(out, o)
	}
	return out, rows.Err()
}

// InsertChangeEvents stores a batch of change events.
func (s *Store) InsertChangeEvents(ctx context.Context, events []domain.ChangeEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	q := "INSERT INTO change_events (" + strings.Join(changeCols, ", ") + ") VALUES " + placeholders(len(changeCols))
	for _, e := range events {
		if _, err := tx.ExecContext(ctx, q,
			e.StationID, e.Group, e.Model, e.Field, e.Old, e.New, e.DeltaAbs, e.DeltaPct,
			e.ObservedAt.Unix(), e.Severity,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListChangeEvents returns the most recent change events for a station.
func (s *Store) ListChangeEvents(ctx context.Context, stationID string, limit int) ([]domain.ChangeEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station_id, group_name, model_name, field, old_value, new_value, delta_abs, delta_pct, observed_at, severity
		FROM change_events WHERE station_id=? ORDER BY observed_at DESC LIMIT ?`, stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ChangeEvent
	for rows.Next() {
		var e domain.ChangeEvent
		var ts int64
		if err := rows.Scan(&e.StationID, &e.Group, &e.Model, &e.Field, &e.Old, &e.New,
			&e.DeltaAbs, &e.DeltaPct, &ts, &e.Severity); err != nil {
			return nil, err
		}
		e.ObservedAt = time.Unix(ts, 0).Local()
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertProbeResult stores one probe result.
func (s *Store) InsertProbeResult(ctx context.Context, r domain.ProbeResult) error {
	du := 0
	if r.DeclaredUnavailable {
		du = 1
	}
	q := "INSERT INTO probe_results (" + strings.Join(probeCols, ", ") + ") VALUES " + placeholders(len(probeCols))
	_, err := s.db.ExecContext(ctx, q,
		r.StationID, r.Model, r.TokensIn, r.TokensOut,
		r.DeclaredNativeRatio, r.DeclaredEffectiveUSDPer1M, r.MeasuredUSDPer1M, r.MarkupPct, r.CostUSD,
		du, r.ObservedAt.Unix(), r.Error,
	)
	return err
}

// ListProbeResults returns the most recent probe results for a station.
func (s *Store) ListProbeResults(ctx context.Context, stationID string, limit int) ([]domain.ProbeResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station_id, model_name, tokens_in, tokens_out,
		declared_native_ratio, declared_effective_usd_per_1m, measured_usd_per_1m, markup_pct, cost_usd,
		declared_unavailable, observed_at, error FROM probe_results WHERE station_id=? ORDER BY observed_at DESC LIMIT ?`,
		stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProbeResult
	for rows.Next() {
		var r domain.ProbeResult
		var du int
		var ts int64
		if err := rows.Scan(&r.StationID, &r.Model, &r.TokensIn, &r.TokensOut,
			&r.DeclaredNativeRatio, &r.DeclaredEffectiveUSDPer1M, &r.MeasuredUSDPer1M, &r.MarkupPct, &r.CostUSD,
			&du, &ts, &r.Error); err != nil {
			return nil, err
		}
		r.DeclaredUnavailable = du == 1
		r.ObservedAt = time.Unix(ts, 0).Local()
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertAuditLog writes one audit entry. Callers must redact secrets in detail.
func (s *Store) InsertAuditLog(ctx context.Context, actor, action, target, detail string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO audit_log (actor, action, target, detail) VALUES (?,?,?,?)",
		actor, action, target, detail)
	return err
}

// ListAuditLogs returns the most recent audit entries.
func (s *Store) ListAuditLogs(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, ts, actor, action, target, detail FROM audit_log ORDER BY ts DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		if err := rows.Scan(&e.ID, &e.Ts, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		e.Ts = e.Ts.Local() // SQLite CURRENT_TIMESTAMP is UTC; render in the configured tz.
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertStation persists a station (config blob + encrypted creds). encKey may be nil
// (then creds are not stored). Used to seed YAML stations + web-added stations.
func (s *Store) UpsertStation(ctx context.Context, st domain.Station, encKey []byte) error {
	blob, err := json.Marshal(st) // AuthConfig fields are json:"-" → creds omitted from blob
	if err != nil {
		return err
	}
	tags := strings.Join(st.Tags, ",")
	enabled := 0
	if st.Enabled {
		enabled = 1
	}
	pollSec := int64(time.Duration(st.PollInterval) / time.Second)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO stations (id,name,kind,base_url,config_yaml,poll_interval_seconds,tags,enabled,sort_order)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind,
		base_url=excluded.base_url, config_yaml=excluded.config_yaml, poll_interval_seconds=excluded.poll_interval_seconds,
		tags=excluded.tags, enabled=excluded.enabled, sort_order=excluded.sort_order, updated_at=CURRENT_TIMESTAMP`,
		st.ID, st.Name, string(st.Kind), st.BaseURL, string(blob), pollSec, tags, enabled, st.SortOrder); err != nil {
		return err
	}
	if encKey == nil {
		return nil
	}
	credsBlob, err := yaml.Marshal(st.Auth)
	if err != nil {
		return err
	}
	ct, nonce, err := secrets.Encrypt(encKey, credsBlob)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO credentials (station_id, ciphertext, nonce) VALUES (?,?,?)
		ON CONFLICT(station_id) DO UPDATE SET ciphertext=excluded.ciphertext, nonce=excluded.nonce, updated_at=CURRENT_TIMESTAMP`,
		st.ID, ct, nonce)
	return err
}

// StationLoadFailure records a station whose config blob loaded but whose
// encrypted credentials could not be decrypted (encKey mismatch or corruption).
// The station is still returned in the slice (with empty Auth) so polling can
// surface a clear "no credentials loaded" error rather than silently ingesting
// new-api's built-in default catalog as if it were the station's real catalog.
type StationLoadFailure struct {
	StationID string
	Reason    string // e.g. "decrypt_failed"
	Err       error
}

// ListStationsDB loads all persisted stations (config blob + decrypted creds).
// Stations whose creds fail to decrypt are still returned (Auth zero-value)
// AND reported in the failures slice — callers must surface this, not swallow
// it, or the station silently polls with no credentials and the operator only
// sees a misleading downstream "no api_key" error.
func (s *Store) ListStationsDB(ctx context.Context, encKey []byte) ([]domain.Station, []StationLoadFailure, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, config_yaml FROM stations ORDER BY sort_order, id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []domain.Station
	var fails []StationLoadFailure
	for rows.Next() {
		var st domain.Station
		var id, blob string
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal([]byte(blob), &st); err != nil {
			continue
		}
		st.ID = id
		if encKey != nil {
			var ct, nonce []byte
			if e := s.db.QueryRowContext(ctx, "SELECT ciphertext, nonce FROM credentials WHERE station_id=?", id).Scan(&ct, &nonce); e == nil {
				// A credentials row exists but the key won't decrypt it → encKey
				// mismatch (or corruption). Report it instead of leaving Auth
				// silently empty; otherwise the station polls credential-free and
				// the operator only learns about it via a misleading "no api_key"
				// refusal from the adapter, hiding the real cause.
				if pt, derr := secrets.Decrypt(encKey, ct, nonce); derr != nil {
					st.DecryptFailed = true
					fails = append(fails, StationLoadFailure{StationID: id, Reason: "decrypt_failed", Err: derr})
				} else {
					_ = yaml.Unmarshal(pt, &st.Auth)
				}
			}
		}
		out = append(out, st)
	}
	return out, fails, rows.Err()
}

// DeleteStation removes a station and its (cascade) credentials.
func (s *Store) DeleteStation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM stations WHERE id=?", id)
	return err
}

// ReorderStations sets sort_order for each station according to the position
// in orderedIDs. IDs not in the slice keep sort_order 0.
func (s *Store) ReorderStations(ctx context.Context, orderedIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range orderedIDs {
		if _, err := tx.ExecContext(ctx, "UPDATE stations SET sort_order=? WHERE id=?", i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetStationGroupConfigs returns all per-group display config rows for a station.
// Groups with no row are absent — callers treat absence as visible=true,sort_order=0
// (see domain.PartitionGroups).
func (s *Store) GetStationGroupConfigs(ctx context.Context, stationID string) ([]domain.StationGroupConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT station_id, group_name, visible, sort_order FROM station_group_config WHERE station_id=? ORDER BY sort_order, group_name`,
		stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StationGroupConfig
	for rows.Next() {
		var c domain.StationGroupConfig
		var vis int
		if err := rows.Scan(&c.StationID, &c.GroupName, &vis, &c.SortOrder); err != nil {
			return nil, err
		}
		c.Visible = vis != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertStationGroupConfig inserts or updates a single group's display config.
func (s *Store) UpsertStationGroupConfig(ctx context.Context, cfg domain.StationGroupConfig) error {
	vis := 0
	if cfg.Visible {
		vis = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO station_group_config (station_id, group_name, visible, sort_order) VALUES (?,?,?,?)
		 ON CONFLICT(station_id, group_name) DO UPDATE SET visible=excluded.visible, sort_order=excluded.sort_order`,
		cfg.StationID, cfg.GroupName, vis, cfg.SortOrder)
	return err
}

// SaveStationGroupConfigs replaces the entire per-station config set in one
// transaction (delete-then-insert). Last-write-wins; an empty cfgs slice clears
// all rows for the station.
func (s *Store) SaveStationGroupConfigs(ctx context.Context, stationID string, cfgs []domain.StationGroupConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM station_group_config WHERE station_id=?`, stationID); err != nil {
		return err
	}
	for _, c := range cfgs {
		vis := 0
		if c.Visible {
			vis = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO station_group_config (station_id, group_name, visible, sort_order) VALUES (?,?,?,?)`,
			c.StationID, c.GroupName, vis, c.SortOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteStationGroupConfig removes one group's config row (optional cleanup;
// orphaned rows are harmless since rendering only reads current-poll groups).
func (s *Store) DeleteStationGroupConfig(ctx context.Context, stationID, groupName string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM station_group_config WHERE station_id=? AND group_name=?`, stationID, groupName)
	return err
}

// InsertSnapshot stores a raw snapshot payload.
func (s *Store) InsertSnapshot(ctx context.Context, snap domain.RawSnapshot) error {
	capsJSON := ""
	if len(snap.GroupRatios) > 0 {
		if b, err := json.Marshal(map[string]any{"group_ratios": snap.GroupRatios}); err == nil {
			capsJSON = string(b)
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO snapshots (station_id, observed_at, endpoints, payloads, capabilities) VALUES (?,?,?,?,?)`,
		snap.StationID, snap.ObservedAt.Unix(),
		strings.Join(snap.EndpointsUsed, ","), []byte{}, capsJSON,
	)
	return err
}

// LatestGroupRatios returns the most recently stored group_ratios for a station.
func (s *Store) LatestGroupRatios(ctx context.Context, stationID string) (map[string]float64, error) {
	var capsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT capabilities FROM snapshots WHERE station_id=? AND capabilities != '' ORDER BY observed_at DESC LIMIT 1`,
		stationID).Scan(&capsJSON)
	if err != nil {
		return nil, err
	}
	var caps struct {
		GroupRatios map[string]float64 `json:"group_ratios"`
	}
	if err := json.Unmarshal([]byte(capsJSON), &caps); err != nil {
		return nil, err
	}
	return caps.GroupRatios, nil
}

// PrevGroupRatios returns the most recent group_ratios stored BEFORE `before`
// for a station (nil, nil when none exists yet — e.g. first poll). Used by the
// scheduler to diff against the previous snapshot before inserting the new one.
func (s *Store) PrevGroupRatios(ctx context.Context, stationID string, before time.Time) (map[string]float64, error) {
	var capsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT capabilities FROM snapshots
		 WHERE station_id=? AND capabilities != '' AND observed_at < ?
		 ORDER BY observed_at DESC LIMIT 1`,
		stationID, before.Unix()).Scan(&capsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var caps struct {
		GroupRatios map[string]float64 `json:"group_ratios"`
	}
	if err := json.Unmarshal([]byte(capsJSON), &caps); err != nil {
		return nil, err
	}
	return caps.GroupRatios, nil
}

// GroupRatioHistory returns up to `limit` most recent group-ratio snapshots for
// a station, oldest-first, reconstructed from snapshots.capabilities. Used for
// per-group trend sparklines.
func (s *Store) GroupRatioHistory(ctx context.Context, stationID string, limit int) ([]domain.GroupRatioSnapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT observed_at, capabilities FROM snapshots
		 WHERE station_id=? AND capabilities != ''
		 ORDER BY observed_at DESC LIMIT ?`, stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var desc []domain.GroupRatioSnapshot
	for rows.Next() {
		var ts int64
		var capsJSON string
		if err := rows.Scan(&ts, &capsJSON); err != nil {
			return nil, err
		}
		var caps struct {
			GroupRatios map[string]float64 `json:"group_ratios"`
		}
		if err := json.Unmarshal([]byte(capsJSON), &caps); err != nil {
			continue
		}
		desc = append(desc, domain.GroupRatioSnapshot{
			ObservedAt: time.Unix(ts, 0).Local(), Ratios: caps.GroupRatios,
		})
	}
	// reverse to oldest-first for sparkline left→right
	out := make([]domain.GroupRatioSnapshot, len(desc))
	for i := range desc {
		out[i] = desc[len(desc)-1-i]
	}
	return out, rows.Err()
}

// InsertBalanceObservation stores one per-poll balance observation.
func (s *Store) InsertBalanceObservation(ctx context.Context, ob domain.BalanceObservation) error {
	unlimited := 0
	if ob.Unlimited {
		unlimited = 1
	}
	q := "INSERT INTO balance_observations (" + strings.Join(balanceCols, ", ") + ") VALUES " + placeholders(len(balanceCols))
	_, err := s.db.ExecContext(ctx, q,
		ob.StationID, ob.ObservedAt.Unix(), ob.Remaining, ob.Used, ob.Total,
		ob.RemainingUSD, ob.UsedUSD, ob.TotalUSD, unlimited,
		ob.Currency, ob.QuotaPerUnit, ob.USDExchangeRate, ob.SourceEndpoint,
	)
	return err
}

// scanBalance scans one balance_observations row into a BalanceObservation.
func scanBalance(sc func(...any) error) (domain.BalanceObservation, error) {
	var ob domain.BalanceObservation
	var ts int64
	var unlimited int
	if err := sc(
		&ob.StationID, &ts, &ob.Remaining, &ob.Used, &ob.Total,
		&ob.RemainingUSD, &ob.UsedUSD, &ob.TotalUSD, &unlimited,
		&ob.Currency, &ob.QuotaPerUnit, &ob.USDExchangeRate, &ob.SourceEndpoint,
	); err != nil {
		return ob, err
	}
	ob.ObservedAt = time.Unix(ts, 0).Local()
	ob.Unlimited = unlimited == 1
	return ob, nil
}

// LatestBalance returns the most recent balance observation for a station.
// Returns (zero, sql.ErrNoRows) when none exists.
func (s *Store) LatestBalance(ctx context.Context, stationID string) (domain.BalanceObservation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT station_id, observed_at, remaining, used, total,
		remaining_usd, used_usd, total_usd, unlimited, currency, quota_per_unit, usd_exchange_rate, source_endpoint
		FROM balance_observations WHERE station_id=? ORDER BY observed_at DESC, id DESC LIMIT 1`, stationID)
	return scanBalance(row.Scan)
}

// PrevBalance returns the most recent balance observation stored BEFORE `before`
// for a station (zero, sql.ErrNoRows when none). Used by the scheduler to diff
// the quota_drop_pct alert against the prior reading, before inserting the new one.
// The id tie-breaker makes the "previous" row deterministic when two readings
// share the same observed_at second (e.g. a manual PollNow racing a scheduled tick).
func (s *Store) PrevBalance(ctx context.Context, stationID string, before time.Time) (domain.BalanceObservation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT station_id, observed_at, remaining, used, total,
		remaining_usd, used_usd, total_usd, unlimited, currency, quota_per_unit, usd_exchange_rate, source_endpoint
		FROM balance_observations WHERE station_id=? AND observed_at < ? ORDER BY observed_at DESC, id DESC LIMIT 1`,
		stationID, before.Unix())
	return scanBalance(row.Scan)
}

// BalanceHistory returns up to `limit` most recent balance observations for a
// station, oldest-first (for sparkline left→right rendering).
func (s *Store) BalanceHistory(ctx context.Context, stationID string, limit int) ([]domain.BalanceObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station_id, observed_at, remaining, used, total,
		remaining_usd, used_usd, total_usd, unlimited, currency, quota_per_unit, usd_exchange_rate, source_endpoint
		FROM balance_observations WHERE station_id=? ORDER BY observed_at DESC LIMIT ?`, stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var desc []domain.BalanceObservation
	for rows.Next() {
		ob, err := scanBalance(rows.Scan)
		if err != nil {
			return nil, err
		}
		desc = append(desc, ob)
	}
	out := make([]domain.BalanceObservation, len(desc))
	for i := range desc {
		out[i] = desc[len(desc)-1-i]
	}
	return out, rows.Err()
}

// LatestBalances returns the most recent balance observation per station (for
// the /balance overview page). Stations with no balance reading are omitted.
// The id tie-breaker keeps one row per station even when two readings share the
// same observed_at second (a manual PollNow racing a scheduled tick).
func (s *Store) LatestBalances(ctx context.Context) ([]domain.BalanceObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station_id, observed_at, remaining, used, total,
		remaining_usd, used_usd, total_usd, unlimited, currency, quota_per_unit, usd_exchange_rate, source_endpoint
		FROM balance_observations o
		WHERE id = (SELECT id FROM balance_observations WHERE station_id = o.station_id ORDER BY observed_at DESC, id DESC LIMIT 1)
		ORDER BY station_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BalanceObservation
	for rows.Next() {
		ob, err := scanBalance(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, ob)
	}
	return out, rows.Err()
}

// DownsampleAndRetain deletes old snapshots (older than snapshotDays) and
// aggregates ratio_observations older than obsDays into hourly buckets, then
// deletes the aggregated raw rows. Idempotent.
func (s *Store) DownsampleAndRetain(ctx context.Context, now time.Time, snapshotDays, obsDays int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	snapCutoff := now.AddDate(0, 0, -snapshotDays).Unix()
	obsCutoff := now.AddDate(0, 0, -obsDays).Unix()

	if _, err := tx.ExecContext(ctx, "DELETE FROM snapshots WHERE observed_at < ?", snapCutoff); err != nil {
		return err
	}
	// Balance time series rides the snapshot retention window (one row per
	// poll, same cadence as snapshots); older rows are dropped wholesale.
	if _, err := tx.ExecContext(ctx, "DELETE FROM balance_observations WHERE observed_at < ?", snapCutoff); err != nil {
		return err
	}

	// Aggregate old raw observations into hourly buckets (upsert).
	if _, err := tx.ExecContext(ctx, `INSERT INTO ratio_observations_hourly
		(station_id, group_name, model_name, hour, avg_input, min_input, max_input, avg_output, min_output, max_output)
		SELECT station_id, group_name, model_name,
		       strftime('%Y-%m-%d %H', observed_at, 'unixepoch') AS h,
		       AVG(input_usd_per_1m), MIN(input_usd_per_1m), MAX(input_usd_per_1m),
		       AVG(output_usd_per_1m), MIN(output_usd_per_1m), MAX(output_usd_per_1m)
		FROM ratio_observations
		WHERE observed_at < ? AND sentinel = ''
		GROUP BY station_id, group_name, model_name, h
		ON CONFLICT(station_id, group_name, model_name, hour) DO UPDATE SET
			avg_input=excluded.avg_input, min_input=excluded.min_input, max_input=excluded.max_input,
			avg_output=excluded.avg_output, min_output=excluded.min_output, max_output=excluded.max_output`,
		obsCutoff); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM ratio_observations WHERE observed_at < ?", obsCutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// SetCredentials encrypts and stores credentials for a station (upsert).
func (s *Store) SetCredentials(ctx context.Context, stationID string, key []byte, plaintext string) error {
	ct, nonce, err := secrets.Encrypt(key, []byte(plaintext))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO credentials (station_id, ciphertext, nonce, updated_at) VALUES (?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(station_id) DO UPDATE SET ciphertext=excluded.ciphertext, nonce=excluded.nonce, updated_at=CURRENT_TIMESTAMP`,
		stationID, ct, nonce)
	return err
}

// GetCredentials decrypts and returns stored credentials.
func (s *Store) GetCredentials(ctx context.Context, stationID string, key []byte) (string, error) {
	var ct, nonce []byte
	err := s.db.QueryRowContext(ctx, "SELECT ciphertext, nonce FROM credentials WHERE station_id=?", stationID).Scan(&ct, &nonce)
	if err != nil {
		return "", err
	}
	pt, err := secrets.Decrypt(key, ct, nonce)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// RotationResult reports the outcome of a key rotation pass.
type RotationResult struct {
	StationsRotated   int
	NotifiersRotated  int
	FailedStationIDs  []string // could not decrypt with oldKey (wrong key / corruption)
	FailedNotifierIDs []string
}

// RotateKey re-encrypts every encrypted blob from oldKey to newKey across both
// the credentials and notifier_config tables. Rows that fail to decrypt with
// oldKey are skipped and listed in the result (so the operator can re-enter
// those credentials); the rotation proceeds for the rest. Use via
// `transitmonitor -rotate-key -old-key <K> -new-key <K2>`.
func (s *Store) RotateKey(ctx context.Context, oldKey, newKey []byte) (RotationResult, error) {
	var res RotationResult

	// credentials table (per-station AuthConfig YAML).
	rows, err := s.db.QueryContext(ctx, "SELECT station_id, ciphertext, nonce FROM credentials")
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		var ct, nonce []byte
		if err := rows.Scan(&id, &ct, &nonce); err != nil {
			rows.Close()
			return res, err
		}
		pt, derr := secrets.Decrypt(oldKey, ct, nonce)
		if derr != nil {
			res.FailedStationIDs = append(res.FailedStationIDs, id)
			continue
		}
		nct, nnonce, err := secrets.Encrypt(newKey, pt)
		if err != nil {
			rows.Close()
			return res, fmt.Errorf("re-encrypt station %s: %w", id, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE credentials SET ciphertext=?, nonce=?, updated_at=CURRENT_TIMESTAMP WHERE station_id=?`,
			nct, nnonce, id); err != nil {
			rows.Close()
			return res, err
		}
		res.StationsRotated++
	}
	rows.Close()

	// notifier_config table (DingTalk/webhook secrets, etc.).
	nrows, err := s.db.QueryContext(ctx, "SELECT id, ciphertext, nonce FROM notifier_config")
	if err != nil {
		return res, err
	}
	for nrows.Next() {
		var id string
		var ct, nonce []byte
		if err := nrows.Scan(&id, &ct, &nonce); err != nil {
			nrows.Close()
			return res, err
		}
		pt, derr := secrets.Decrypt(oldKey, ct, nonce)
		if derr != nil {
			res.FailedNotifierIDs = append(res.FailedNotifierIDs, id)
			continue
		}
		nct, nnonce, err := secrets.Encrypt(newKey, pt)
		if err != nil {
			nrows.Close()
			return res, fmt.Errorf("re-encrypt notifier %s: %w", id, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE notifier_config SET ciphertext=?, nonce=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			nct, nnonce, id); err != nil {
			nrows.Close()
			return res, err
		}
		res.NotifiersRotated++
	}
	nrows.Close()
	return res, nil
}

// NotifierConfigID is the single row key used for the encrypted notifier blob.
const NotifierConfigID = "notifiers"

// SetNotifierConfig encrypts and stores the JSON notifier config (upsert).
// Returns ErrEncryptionDisabled if key is nil (no TRANSMONITOR_ENCRYPTION_KEY).
func (s *Store) SetNotifierConfig(ctx context.Context, id string, key []byte, plaintext string) error {
	if key == nil {
		return ErrEncryptionDisabled
	}
	ct, nonce, err := secrets.Encrypt(key, []byte(plaintext))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO notifier_config (id, ciphertext, nonce, updated_at) VALUES (?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET ciphertext=excluded.ciphertext, nonce=excluded.nonce, updated_at=CURRENT_TIMESTAMP`,
		id, ct, nonce)
	return err
}

// GetNotifierConfig decrypts and returns the stored notifier config JSON.
// Returns ("", sql.ErrNoRows) when none is stored, and ErrEncryptionDisabled
// when key is nil.
func (s *Store) GetNotifierConfig(ctx context.Context, id string, key []byte) (string, error) {
	if key == nil {
		return "", ErrEncryptionDisabled
	}
	var ct, nonce []byte
	err := s.db.QueryRowContext(ctx, "SELECT ciphertext, nonce FROM notifier_config WHERE id=?", id).Scan(&ct, &nonce)
	if err != nil {
		return "", err
	}
	pt, err := secrets.Decrypt(key, ct, nonce)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// SetAppSetting upserts a plain-text key-value pair in app_settings.
func (s *Store) SetAppSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES (?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`, key, value)
	return err
}

// GetAppSetting reads a plain-text setting. Returns ("", false, nil) when the
// key does not exist.
func (s *Store) GetAppSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key=?", key).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

// AlertEventRow is a persisted alert event (for the /alerts page).
type AlertEventRow struct {
	ID        int64
	Ts        time.Time
	Rule      string
	StationID string
	Model     string
	Payload   string
	Sent      bool
	Error     string
}

func (s *Store) InsertAlertEvent(ctx context.Context, rule, stationID, model, payload string, sent bool, errMsg string) error {
	sentInt := 0
	if sent {
		sentInt = 1
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO alert_events (rule_id, station_id, model_name, payload, sent, error) VALUES (?,?,?,?,?,?)", rule, stationID, model, payload, sentInt, errMsg)
	return err
}

func (s *Store) ListAlertEvents(ctx context.Context, limit int) ([]AlertEventRow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, created_at, rule_id, station_id, model_name, payload, sent, error FROM alert_events ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEventRow
	for rows.Next() {
		var r AlertEventRow
		var sent int
		if err := rows.Scan(&r.ID, &r.Ts, &r.Rule, &r.StationID, &r.Model, &r.Payload, &sent, &r.Error); err != nil {
			return nil, err
		}
		r.Sent = sent == 1
		r.Ts = r.Ts.Local() // SQLite CURRENT_TIMESTAMP is UTC; render in the configured tz.
		out = append(out, r)
	}
	return out, rows.Err()
}

// ObservationHistory returns the last N observations for a (station, model) for sparkline rendering.
func (s *Store) ObservationHistory(ctx context.Context, stationID, modelName string, limit int) ([]domain.RatioObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station_id, group_name, model_name, native_ratio, native_ratio_kind, quota_type,
		input_usd_per_1m, output_usd_per_1m, cache_read_usd_per_1m, cache_write_usd_per_1m,
		fixed_price_usd, completion_ratio, peak_info, declared_unavailable, sentinel, note,
		observed_at, source_endpoint
		FROM ratio_observations WHERE station_id=? AND model_name=? ORDER BY observed_at DESC LIMIT ?`, stationID, modelName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RatioObservation
	for rows.Next() {
		var o domain.RatioObservation
		var du int
		var ts int64
		if err := rows.Scan(&o.StationID, &o.GroupName, &o.ModelName, &o.NativeRatio, &o.NativeRatioKind, &o.QuotaType,
			&o.InputUSDPer1M, &o.OutputUSDPer1M, &o.CacheReadUSDPer1M, &o.CacheWriteUSDPer1M,
			&o.FixedPriceUSD, &o.CompletionRatio, &o.PeakInfo, &du, &o.Sentinel, &o.Note, &ts, &o.SourceEndpoint); err != nil {
			return nil, err
		}
		o.DeclaredUnavailable = du == 1
		o.ObservedAt = time.Unix(ts, 0).Local()
		out = append(out, o)
	}
	return out, rows.Err()
}

// CountPollErrors counts audit_log entries with action="poll.error" for a station.
func (s *Store) CountPollErrors(ctx context.Context, stationID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log WHERE action=? AND target=?", "poll.error", stationID).Scan(&count)
	return count, err
}
