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
| `TRANSMONITOR_DASHBOARD_PUBLIC` | 设为 `1` 则**免鉴权**（demo / 反代前置；仅用于可信网络） | 空 |
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
| `GET /` | HTML 总览（站列表 + 导航）|
| `GET /matrix` | HTML 跨站有效 USD/1M 矩阵 |
| `GET /changes?station=` | HTML 变更事件表 |
| `GET /probes?station=` | HTML 探测结果表（含 markup）|
| `GET /audit` | HTML 审计日志 |
| `GET /metrics` | Prometheus 指标（免鉴权，供 Prom 抓取）|
| `GET /healthz` | 健康检查（免鉴权，供 Docker/k8s）|
| `GET /api/stations` | 站列表（凭据脱敏，JSON）|
| `GET /api/ratios?station=` | 某站最新归一化倍率 |
| `GET /api/changes?station=` | 某站变更事件流 |
| `GET /api/probes?station=` | 某站探测结果（含 markup）|
| `GET /api/matrix?model=` | 跨站有效 USD/1M 矩阵（不可派生行带 sentinel 标签）|
| `GET /api/audit` | 审计日志 |

Prometheus 指标：`transitmonitor_input_usd_per_1m{station,group,model}`、`transitmonitor_output_usd_per_1m{...}`、`transitmonitor_probe_markup_pct{station,model}`（excluded/sentinel 行跳过）。

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
- sub2api：分组倍率取自 `/api/v1/groups/available`（user JWT，非管理员可拿全部分组倍率）；逐模型价 = 公开 LiteLLM 表 × group 倍率（运行时自动拉取 `BerriAI/litellm` 价表，~24h 刷新，离线用内置样本兜底）。`/v1/models` 列模型清单需 sk-key 有余额（无余额则 403 `INSUFFICIENT_BALANCE`，降级为 `missing-base-price`）。站长渠道自定义覆写价只有 admin 开 available-channels 才拿得到——否则靠真实成本探测抓 markup。simple 模式（billing 404）→ `declared-unavailable (simple mode)`。

## 6. 告警规则
`type` ∈ `delta_pct` | `delta_abs` | `model_added` | `model_removed` | `probe_markup_pct` | `endpoint_auth_failed` | `poll_failure_streak`。`threshold` 对应阈值；`enabled:false` 不触发。
- 钉钉：`alerts.dingtalk.webhook` + `secret` → HMAC-SHA256 签名 markdown。
- 通用 webhook：`alerts.webhook.url` → POST JSON `{station,model,field,old,new,delta_pct,severity,...}`。

## 7. 真实成本探测
站 `probe.enabled:true` 时，对 `probe.models`（或单模型 `probe.model`）逐个发**非流式**最小 chat 请求，读 usage delta，反推真实有效倍率并对账 markup：
- new-api：`/v1/dashboard/billing/usage`（sk-key，`total_usage` cents）前后 delta。
- sub2api：`/v1/usage`（`total.actual_cost`）前后 delta。
护栏：`max_input_tokens`/`max_output_tokens` 硬上限、`max_cost_cents_per_run` 预估拦截、`dry_run` 不发只记声明成本、`(station,model)` 10min 去重。结果在 `/api/probes?station=`，审计在 `/api/audit`。**先 `dry_run:true` 确认成本再开。**
- `probe.interval`（如 `24h`）：探测走**独立低频节奏**，与 `poll_interval` 解耦——探测要花钱，频率通常很低。留空则随每次 poll 探测（旧行为）。
- 用途：sub2api 站长若对渠道做了**自定义定价覆写**，声明价（LiteLLM×倍率）与实测价会偏差，靠 probe 抓 markup。

## 8. 保留与降采样
- `snapshots` 保留 7 天；`ratio_observations` 保留 30 天，超期按 `(station,model,hour)` 聚合 avg/min/max。
- 调度内置每日保留任务（`scheduler.SetRetention(7,30)` 可改）。

## 9. Docker 部署
`docker-compose.yml`：
```yaml
services:
  transitmonitor:
    image: ghcr.io/yang-yang9/transitmonitor:v0.0.1   # 预构建多架构镜像；本地构建改 build: .
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
TRANSMONITOR_DASHBOARD_TOKEN=xxx TRANSMONITOR_ENCRYPTION_KEY=xxx docker compose up -d   # 拉取预构建镜像
docker compose logs -f           # 看 slog JSON 日志
curl -H "Authorization: Bearer xxx" http://localhost:7421/api/stations
```
多架构镜像（CI 在打 `v*` tag 时自动 buildx push `ghcr.io/yang-yang9/transitmonitor:<tag>` + `:latest`，amd64+arm64）：
```bash
docker pull ghcr.io/yang-yang9/transitmonitor:v0.0.1   # 或 :latest 跟最新
# 本地 buildx：docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/yang-yang9/transitmonitor:latest --push .
```
> 注意：容器绑定 `0.0.0.0:7421`，**必须设 `TRANSMONITOR_DASHBOARD_TOKEN`** 否则外部访问被 401（healthz 走 localhost 免鉴权）。

## 10. 备份 / 迁移
SQLite 单文件 `transitmonitor.db`（+ `-wal`/`-shm`）。停服后复制即可。Docker：数据在 `tm-data` 卷 / `/data`。

## 9b. 在面板内更新 / 回退（sub2api 风格）
打开 `/system` 页（导航栏「系统」）即可看到当前版本、最新 Release、回退候选与三个按钮：**立即更新** / **回退** / **重启**。

工作原理（仿 sub2api）：
- 点击「立即更新」→ 后端打 `api.github.com/repos/yang-yang9/TransitMonitor/releases/latest`（20 分钟缓存，可 `?force=true` 强制刷新），下载对应 `linux_amd64`/`linux_arm64` 的 tar.gz + `checksums.txt`，校验 SHA256，从 tar.gz 解出 `transitmonitor` 二进制（Zip-Slip 守卫），**原子 rename** 换盘（旧二进制按版本号归档为回退候选，保留最近 3 份）。完成后返回 `need_restart:true`。
- 点击「重启」→ 裸二进制模式用 `syscall.Exec` 原地替换进程映像（保留 PID，Exec 前先 flush+close SQLite WAL）；Docker 模式 `os.Exit(0)`，由 `restart: unless-stopped` + wrapper entrypoint 重新拉起。wrapper 优先 exec `/data/bin/transitmonitor`（面板更新写入的持久路径），没有则回退镜像内 `/app/transitmonitor`——**这样新二进制活过容器重建**。
- 「回退」下拉选一个本地归档版本 → 恢复该二进制并提示重启。

前置条件与注意事项：
- **仅 Release 构建可自更新**：`make build` 产物 version=`0.1.0-dev`，会被当作比任何 tag 都旧（`dev` 在版本比较里等于 0.0.0），所以仍能提示有新版本；但自更新建议跑 Release 资产。
- **Docker 用户需先拉一次含 wrapper 的镜像（v0.0.2+）**。v0.0.1 镜像没有 wrapper，面板内更新后容器重建会回退到旧镜像二进制——`/system` 页会检测 wrapper 是否就位，未就位时禁用「立即更新」并提示重新拉镜像。
- 下载校验：仅允许 `github.com` / `*.githubusercontent.com` 的 HTTPS（SSRF 守卫），`io.LimitReader` 500MB 上限。
- 可选环境变量：`TRANSMONITOR_UPDATE_GITHUB_TOKEN`（GitHub Bearer，提高 API 速率限制，仅发给 `api.github.com`）；`HTTP_PROXY`/`HTTPS_PROXY`（Go 默认支持，内网访问 GitHub 走代理出口）。**注意：仓库为 private 时必须设此 token（需 `repo` scope），否则 GitHub Releases API 返回 404。**
- 裸二进制模式下回退备份落在二进制同目录的 `.transitmonitor-backups/`；Docker 模式落在 `/data/.updates/backup/`。

API：`GET /api/system/version`、`GET /api/system/check-updates[?force=true]`、`GET /api/system/rollback-versions`、`POST /api/system/upgrade`、`POST /api/system/rollback`（body 可选 `{"version":"x.y.z"}`）、`POST /api/system/restart`。均受 dashboard 鉴权保护（token / localhost / `TRANSMONITOR_DASHBOARD_PUBLIC`）。

## 11. 安全
- 凭据 `AuthConfig` 字段 `json:"-"`，dashboard 响应/日志经 `secrets.Redact` 掩码（`sk-***`）。
- 设 `TRANSMONITOR_ENCRYPTION_KEY` 后，启动时各站凭据 AES-GCM 加密写入 `credentials` 表（明文不入库）。
- 抓取只读 GET；唯一写站动作 = 真实成本探测（受护栏、可干跑、可停、去重）。
- ⚠ `TRANSMONITOR_ENCRYPTION_KEY` 是**根密钥**：必须和 DB 一起持久化（compose `environment` / `.env`（不入库）/ secret manager）并备份。**丢失它 = 已存凭据永久不可恢复**。Web UI 加的站用当时生效的 key 加密；重启时 key 不一致，站会以空凭据加载，dashboard 标红「凭据解密失败」徽标，需到站点管理页重新录入凭据（用当前 key 重新加密）。轮换 key 不丢凭据：
  ```bash
  ./transitmonitor -rotate-key -old-key <旧key> -new-key <新key>
  # 或 -new-key 留空，默认取 TRANSMONITOR_ENCRYPTION_KEY
  ```
  完成后用新 key 重启即可。

## 12. 排查
- 日志：slog JSON → stderr（Docker `docker compose logs -f`）。`TRANSMONITOR_LOG_LEVEL=debug` 看详情。
- `-selftest` 失败 → 读报错（自测覆盖全链路）。
- 站抓不到 → `/api/audit` 看探测记录；日志看 `poll` 错误。常见：
  - `no ratio source available`：new-api pricing 鉴权失败 + ratio_config 关；或 sub2api 无 sk-key/JWT。检查凭据/网络。
  - 站点列表/详情页红色「凭据解密失败」徽标 / 审计 `creds.decrypt_failed` → 当前 `TRANSMONITOR_ENCRYPTION_KEY` 与加站时不一致。重新录入凭据或用原 key 重启。
  - `declared-unavailable (simple mode)`：sub2api simple 模式，billing 不可用。
  - `unconfigured-37.5`：new-api self-use 模式下未配置模型，非真实价。
  - `cost-guardrail-exceeded`：探测预估成本超 `max_cost_cents_per_run`。
  - `no-quota-delta`：探测 usage 前后无变化（结算滞后或 key 被并发消耗）。

## 13. CI / 发布
- `.github/workflows/ci.yml`：push/PR 跑 gofmt/vet/test-race/build/selftest。
- `.github/workflows/docker.yml`：打 `v*` tag 时 buildx 多架构推 ghcr.io。
- 本地 `make test-race vet fmt-check selftest` 即完整本地 CI。
