# TransitMonitor API Reference

## HTML Pages
| Method | Path | Description |
|---|---|---|
| GET | / | Overview (station cards) |
| GET | /matrix | Cross-station effective USD/1M matrix |
| GET | /changes?station= | Change events per station |
| GET | /probes?station= | Probe results per station |
| GET | /alerts | Alert delivery history |
| GET | /audit | Audit log |
| GET | /stations | Station management (list + add/delete) |
| GET | /stations/new | Add-station form |
| GET | /stations/{id} | Station detail (ratios + changes + probes) |
| GET | /stations/{id}/edit | Edit-station form |

## JSON API
| Method | Path | Description |
|---|---|---|
| GET | /api/stations | List stations (creds redacted) |
| GET | /api/ratios?station=X | Latest normalized ratios |
| GET | /api/changes?station=X | Change events |
| GET | /api/probes?station=X | Probe results |
| GET | /api/matrix?model=X&field=input | Cross-station matrix (field: input/output/cache_read/cache_write) |
| GET | /api/matrix?format=csv | Matrix as CSV download |
| GET | /api/audit | Audit log entries |
| POST | /api/stations | Create station (JSON body) |
| PUT | /api/stations/{id} | Update station (JSON body) |
| DELETE | /api/stations/{id} | Delete station |

## Health & Metrics
| Method | Path | Description |
|---|---|---|
| GET | /healthz | Liveness (always 200) |
| GET | /readyz | Readiness (checks DB) |
| GET | /metrics | Prometheus exposition format |

## Prometheus Metrics
- `transitmonitor_input_usd_per_1m{station,group,model}` — effective input USD/1M
- `transitmonitor_output_usd_per_1m{station,group,model}` — effective output USD/1M
- `transitmonitor_probe_markup_pct{station,model}` — hidden markup %

## Auth
- `TRANSMONITOR_DASHBOARD_PUBLIC=1` → no auth (demo/proxy-fronted).
- `TRANSMONITOR_DASHBOARD_TOKEN=xxx` → require `Authorization: Bearer xxx` for non-localhost.
- Default: localhost-only.
