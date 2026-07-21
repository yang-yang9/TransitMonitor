---
name: transitmonitor
description: Operate and deploy TransitMonitor — a Go relay-station 倍率 (ratio) monitor for new-api/sub2api. Use when the user asks to build, run, configure, deploy via Docker, alert (DingTalk/webhook), run the real-cost probe, interpret the dashboard API/logs, or troubleshoot TransitMonitor. Also use when the user mentions 中转站倍率监控, relay-station ratio monitoring, or the TransitMonitor project.
---

# TransitMonitor — operational skill

TransitMonitor monitors LLM relay-station (中转站) 倍率 for **new-api** and **sub2api**.
Scrape → normalize to effective USD/1M token → store (SQLite) → change-detect → alert → dashboard;
plus a real-cost probe that sends a tiny real chat to expose hidden markup.

**Repo layout:** `cmd/transitmonitor` (binary) · `internal/{config,domain,adapter/{,newapi,sub2api},normalize,changedet,probe,scheduler,store,secrets,alert,dashboard,e2e}` · `openspec/` (SDD specs) · `docs/{design,upstream-contract,usage}.md`.

## Build & verify (needs Go 1.22+)
```bash
make build && make test-race && make vet && make fmt-check && make selftest
```
`make selftest` runs an in-process E2E (two mock stations → scrape→normalize→store→change→alert→probe). Must print `self-test PASSED`.

## Run modes
- `./transitmonitor -selftest` — self-test, exit 0/1.
- `./transitmonitor -config config.yaml -once` — one scrape per station, print, exit.
- `./transitmonitor -config config.yaml` — serve dashboard + poll loop + daily retention; Ctrl-C/SIGTERM graceful shutdown.
- `./transitmonitor -version`.

## Config (`config.yaml`; copy from `config.example.yaml`)
- `stations[]`: `id, name, base_url, kind(newapi|sub2api), auth{pat,api_key,admin_api_key,jwt,group}, poll_interval(≥2m), probe{enabled,model,max_input_tokens,max_output_tokens,max_cost_cents_per_run,dry_run}`.
- `alerts.rules[]`: type ∈ `delta_pct|delta_abs|model_added|model_removed|probe_markup_pct|endpoint_auth_failed|poll_failure_streak`, `threshold`, `enabled`.
- `alerts.dingtalk{webhook,secret}` (HMAC-signed markdown) and/or `alerts.webhook.url` (POST JSON).
- Env overrides: `TRANSMONITOR_CONFIG|DB_PATH|DASHBOARD_ADDR|DASHBOARD_TOKEN|ENCRYPTION_KEY|LOG_LEVEL`.

## new-api vs sub2api (key facts)
- new-api: `/api/pricing` is **public by default** (no PAT needed); `/api/ratio_config` needs `ExposeRatioEnabled` (else 403 → adapter falls back to pricing); PAT only for `/api/user/self/groups` + `/api/option`. Probe reads `/v1/dashboard/billing/usage` (sk-key, `total_usage` cents). self-use mode returns `37.5` for unknown models → adapter treats as sentinel.
- sub2api: `/v1/sub2api/billing` (sk-key) gives `effective_rate_multiplier`; 404 = simple mode → `declared-unavailable`. Per-model per-token USD prices from `/api/v1/channels/available` (user JWT + feature flag). Probe reads `/v1/usage` `total.actual_cost`.

## Dashboard (`http://<addr>:7421`)
`/` · `/healthz` (no auth) · `/api/stations` · `/api/ratios?station=` · `/api/changes?station=` · `/api/probes?station=` · `/api/matrix?model=` · `/api/audit`. `token=""` → localhost-only; set `TRANSMONITOR_DASHBOARD_TOKEN` for remote (Bearer).

## Docker deploy (any machine)
```bash
docker build -t transitmonitor:latest .
# config.yaml is read-only mount; data (sqlite) persists in a volume
docker run -d -p 7421:7421 \
  -v "$PWD/config.yaml:/config/config.yaml:ro" -v transitmonitor-data:/data \
  -e TRANSMONITOR_DASHBOARD_TOKEN=secret \
  transitmonitor:latest
```
Or `docker compose up -d --build` (uses `docker-compose.yml`). Multi-arch (amd64+arm64):
`docker buildx build --platform linux/amd64,linux/arm64 -t transitmonitor:latest --push .`
⚠ Container binds `0.0.0.0:7421` → **must set `TRANSMONITOR_DASHBOARD_TOKEN`** for external access.

## Troubleshooting
- Logs: slog JSON → stderr (`docker compose logs -f`; `TRANSMONITOR_LOG_LEVEL=debug`).
- `no ratio source available` → wrong creds / network / pricing auth-gated + ratio_config off.
- `declared-unavailable (simple mode)` → sub2api simple mode.
- `unconfigured-37.5` → new-api self-use, not a real price.
- `cost-guardrail-exceeded` → raise `probe.max_cost_cents_per_run` or lower tokens.
- Probe first with `dry_run: true`.

## Full reference
`docs/usage.md` (manual), `docs/design.md` (architecture), `docs/upstream-contract.md` (endpoint/field cheatsheet), `openspec/changes/add-ratio-monitor-core/specs/` (SDD specs with WHEN/THEN scenarios).
