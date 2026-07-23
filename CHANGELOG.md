# Changelog

## [Unreleased]
### Added
- Web station management: add/edit/delete stations via UI (/stations, /stations/new, /stations/{id}/edit)
- Auto-generate station id (st-<8hex>) when left blank
- Dark mode + 中/EN i18n toggle (client-side, persisted)
- Auto-refresh toggle (60s interval, localStorage)
- Favicon (inline SVG teal TM)
- /readyz readiness endpoint (DB health check, auth-bypass)
- CSV export: GET /api/matrix?format=csv
- Matrix field selector: ?field=input|output|cache_read|cache_write
- Cache price columns (cache_read/cache_write USD/1M) in matrix + API
- HTTP 30s timeout on shared client
- Station CRUD persistence (UpsertStation/ListStationsDB/DeleteStation + encrypted creds)
- Runtime station add/remove (scheduler.AddStation/RemoveStation, per-station poller ctx)
- Prometheus /metrics exporter (input/output USD/1M + probe_markup gauges)
- Per-(model×group) expansion (Group="*")
- Vendored LiteLLM fallback for sub2api (when channels/available unavailable)
- Audit log (/api/audit + HTML page)
- Retention/downsample (snapshots 7d, observations 30d → hourly aggregates)
- Docker multi-stage multi-arch + CI workflows + Claude skill + usage manual
- SDD (openspec spec-driven) + TDD (12 packages, race-clean)
- Real-cost probe (new-api usage-delta + sub2api actual_cost-delta → markup reconciliation)
- DingTalk HMAC-signed + generic webhook notifiers

### Changed
- UI: professional form design (field-label, toggle switch, button hierarchy, card hover)
- UI: sub2api-inspired teal theme + dark mode + blurred orbs + faint grid
- go.mod: go 1.25 (x/time requires it)
- Git identity: Devix <devix@transitmonitor.dev>, no Co-Authored-By trailer

### Fixed
- new-api adapter no longer ingests new-api's full built-in default model list (~2500 entries) when the ratio source is `/api/ratio_config` or `/api/option` and no enabled-filter is available. `/api/pricing` (enabled-channel models only) is now the preferred source; `ratio_config`/`option` results are filtered by `/v1/models` when an `api_key` is set. When neither is available, the adapter refuses with actionable guidance instead of flooding the store with built-in defaults the station never enabled.

## [0.1.0] - 2026-07-21
### Added
- Initial release: 中转站倍率监控 (new-api/sub2api), Go single binary, SQLite, SDD+TDD.
