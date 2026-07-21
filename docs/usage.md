# TransitMonitor 使用手册

> 中转站倍率监控 · 监控 new-api / sub2api 的倍率，归一化为有效 USD/1M token，检测变更、告警、可视化，并对隐藏倍率站做真实成本探测反推 markup。
>
> 单 Go 二进制（无 CGO）· 嵌入式 SQLite · SDD+TDD。

## 1. 安装

### 二进制（本机有 Go 1.22+）
```bash
make build          # 产出 ./transitmonitor
make selftest       # 内置 E2E 自测（mock 站，无需真实站）
```
Go 不在 PATH 时 `make` 自动用 `$HOME/.local/go/bin/go`。

### Docker（任意机器）
```bash
docker build -t transitmonitor:latest .                          # 单架构
docker run --rm transitmonitor:latest -selftest                  # 自测
docker run -d -p 7421:7421 -v "$PWD/config.yaml:/config/config.yaml:ro" \
  -v transitmonitor-data:/data -e TRANSMONITOR_DASHBOARD_TOKEN=secret \
  transitmonitor:latest                                          # 运行
```
或 `docker compose up -d --build`（见 §9）。

## 2. 配置

复制 `config.example.yaml` → `config.yaml`，按需填写。结构：
```yaml
db: { path: transitmonitor.db }
dashboard: { addr: "127.0.0.1:7421", token: "" }
alerts:
  rules: [ {name: price-up-5pct, type: delta_pct, threshold: 5, enabled: true} ]
  dingtalk: { webhook: "https://oapi.dingtalk.com/robot/send?access_token=...", secret: "SEC..." }
  webhook:  { url: "https://hooks.example.com/tm" }
stations: [ ... ]   # 见 §5
```

### 环境变量（覆盖配置/flag，Docker 用）
| 变量 | 说明 | 默认 |
|---|---|---|
| `TRANSMONITOR_CONFIG` | 配置文件路径 | `config.yaml` |
| `TRANSMONITOR_DB_PATH` | SQLite 路径 | `transitmonitor.db` |
| `TRANSMONITOR_DASHBOARD_ADDR` | 监听地址 | `127.0.0.1:7421`（容器内为 `0.0.0.0:7421`）|
| `TRANSMONITOR_DASHBOARD_TOKEN` | 非 localhost 访问所需 bearer token | 空（仅 localhost）|
| `TRANSMONITOR_ENCRYPTION_KEY` | 凭据静态加密密钥（设了则把各站凭据加密入库） | 空 |
| `TRANSMONITOR_LOG_LEVEL` | `debug\|info\|warn\|error` | `info` |

## 3. 运行模式
```bash
./transitmonitor -selftest              # 内置 E2E（双 mock 站全链路 + 探测对账），退出码 0=通过
./transitmonitor -config config.yaml -once   # 每站抓一次，打印，退出
./transitmonitor -config config.yaml    # 常驻：抓取循环 + 每日保留 + dashboard，Ctrl-C/SIGTERM 优雅关停
./transitmonitor -version
```

## 4. Dashboard
默认 `http://127.0.0.1:7421`。`token` 空时仅 localhost；设了则非 localhost 需 `Authorization: Bearer <token>`。

| 端点 | 说明 |
|---|---|
| `GET /` | HTML 总览（站列表）|
| `GET /healthz` | 健康检查（免鉴权，供 Docker/k8s）|
| `GET /api/stations` | 站列表（凭据脱敏）|
| `GET /api/ratios?station=` | 某站最新归一化倍率 |
| `GET /api/changes?station=` | 某站变更事件流 |
| `GET /api/probes?station=` | 某站探测结果（含 markup）|
| `GET /api/matrix?model=` | 跨站有效 USD/1M 矩阵（不可派生行带 sentinel 标签）|
| `GET /api/audit` | 审计日志 |

## 5. 站点配置
```yaml
stations:
  - id: my-relay
    name: My new-api Relay
    base_url: https://relay.example.com
    kind: newapi              # 或 sub2api
    auth:
      pat: "<PAT>"            # new-api 可选：抓 /api/user/self/groups、/api/option
      api_key: "sk-..."       # /v1/* + 探测
      group: default
    poll_interval: 3m         # 下限 2m（尊重站缓存）
    enabled: true
    probe: { enabled: false, model: gpt-4o-mini, max_input_tokens: 8, max_output_tokens: 1, max_cost_cents_per_run: 1, dry_run: true }
  - id: my-sub
    base_url: https://sub.example.com
    kind: sub2api
    auth:
      api_key: "sk-..."        # /v1/* + billing
      jwt: "<user JWT>"        # /api/v1/channels/available（每模型 per-token 价）
      group: default
    poll_interval: 3m
```
- new-api：`/api/pricing` 默认公开（不需 PAT 也能抓）；`/api/ratio_config` 默认关（站需开 `ExposeRatioEnabled`）。
- sub2api：仅 sk-key 时只读自己 key 的 `effective_rate_multiplier`；要每模型价需 JWT + 站开启 available-channels。simple 模式（billing 404）→ 观测标 `declared-unavailable (simple mode)`。

## 6. 告警规则
`type` ∈ `delta_pct` | `delta_abs` | `model_added` | `model_removed` | `probe_markup_pct` | `endpoint_auth_failed` | `poll_failure_streak`。`threshold` 对应阈值；`enabled:false` 不触发。
- 钉钉：`alerts.dingtalk.webhook` + `secret` → HMAC-SHA256 签名 markdown。
- 通用 webhook：`alerts.webhook.url` → POST JSON `{station,model,field,old,new,delta_pct,severity,...}`。

## 7. 真实成本探测
站 `probe.enabled:true` 时，每轮抓取后对 `probe.model` 发一次**非流式**最小 chat 请求，读 usage delta，反推真实有效倍率并对账 markup：
- new-api：`/v1/dashboard/billing/usage`（sk-key，`total_usage` cents）前后 delta。
- sub2api：`/v1/usage`（`total.actual_cost`）前后 delta。
护栏：`max_input_tokens`/`max_output_tokens` 硬上限、`max_cost_cents_per_run` 预估拦截、`dry_run` 不发只记声明成本、`(station,model)` 10min 去重。结果在 `/api/probes?station=`，审计在 `/api/audit`。**先 `dry_run:true` 确认成本再开。**

## 8. 保留与降采样
- `snapshots` 保留 7 天；`ratio_observations` 保留 30 天，超期按 `(station,model,hour)` 聚合 avg/min/max。
- 调度内置每日保留任务（`scheduler.SetRetention(7,30)` 可改）。

## 9. Docker 部署
`docker-compose.yml`：
```yaml
services:
  transitmonitor:
    build: .
    restart: unless-stopped
    ports: ["7421:7421"]
    environment:
      TRANSMONITOR_DASHBOARD_ADDR: 0.0.0.0:7421
      TRANSMONITOR_DASHBOARD_TOKEN: ${TRANSMONITOR_DASHBOARD_TOKEN:-}
      TRANSMONITOR_ENCRYPTION_KEY: ${TRANSMONITOR_ENCRYPTION_KEY:-}
    volumes:
      - ./config.yaml:/config/config.yaml:ro
      - tm-data:/data
    healthcheck: { test: ["CMD","wget","-qO-","http://127.0.0.1:7421/healthz"], interval: 30s }
```
启动：
```bash
cp config.example.yaml config.yaml && $EDITOR config.yaml
TRANSMONITOR_DASHBOARD_TOKEN=xxx TRANSMONITOR_ENCRYPTION_KEY=xxx docker compose up -d --build
docker compose logs -f           # 看 slog JSON 日志
curl -H "Authorization: Bearer xxx" http://localhost:7421/api/stations
```
多架构镜像（CI 在打 tag 时自动 buildx push `ghcr.io/<org>/<repo>:<tag>` + `:latest`，amd64+arm64）：
```bash
docker buildx build --platform linux/amd64,linux/arm64 -t transitmonitor:latest --push .
```
> 注意：容器绑定 `0.0.0.0:7421`，**必须设 `TRANSMONITOR_DASHBOARD_TOKEN`** 否则外部访问被 401（healthz 走 localhost 免鉴权）。

## 10. 备份 / 迁移
SQLite 单文件 `transitmonitor.db`（+ `-wal`/`-shm`）。停服后复制即可。Docker：数据在 `tm-data` 卷 / `/data`。

## 11. 安全
- 凭据 `AuthConfig` 字段 `json:"-"`，dashboard 响应/日志经 `secrets.Redact` 掩码（`sk-***`）。
- 设 `TRANSMONITOR_ENCRYPTION_KEY` 后，启动时各站凭据 AES-GCM 加密写入 `credentials` 表（明文不入库）。
- 抓取只读 GET；唯一写站动作 = 真实成本探测（受护栏、可干跑、可停、去重）。

## 12. 排查
- 日志：slog JSON → stderr（Docker `docker compose logs -f`）。`TRANSMONITOR_LOG_LEVEL=debug` 看详情。
- `-selftest` 失败 → 读报错（自测覆盖全链路）。
- 站抓不到 → `/api/audit` 看探测记录；日志看 `poll` 错误。常见：
  - `no ratio source available`：new-api pricing 鉴权失败 + ratio_config 关；或 sub2api 无 sk-key/JWT。检查凭据/网络。
  - `declared-unavailable (simple mode)`：sub2api simple 模式，billing 不可用。
  - `unconfigured-37.5`：new-api self-use 模式下未配置模型，非真实价。
  - `cost-guardrail-exceeded`：探测预估成本超 `max_cost_cents_per_run`。
  - `no-quota-delta`：探测 usage 前后无变化（结算滞后或 key 被并发消耗）。

## 13. CI / 发布
- `.github/workflows/ci.yml`：push/PR 跑 gofmt/vet/test-race/build/selftest。
- `.github/workflows/docker.yml`：打 `v*` tag 时 buildx 多架构推 ghcr.io。
- 本地 `make test-race vet fmt-check selftest` 即完整本地 CI。
