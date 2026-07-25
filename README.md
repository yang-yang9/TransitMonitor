# TransitMonitor — 中转站倍率监控

独立监控 [new-api](https://github.com/QuantumNous/new-api) / [sub2api](https://github.com/Wei-Shaw/sub2api) 这类 LLM 中转站的"倍率"：周期抓取 → 归一化为可比的**有效 USD/1M token** → 存时序 → 检测变更 → 告警 → 仪表盘可视化；并对**隐藏倍率的站**发真实小请求反推真实有效倍率、对账暴露暗中加价。

**Go 单二进制 · 纯 Go（无 CGO）· 嵌入式 SQLite · SDD + TDD。**

## 为什么

- 中转站**静默改倍率**直接影响真实成本，用户往往事后才发现。
- 两类站倍率体系不同（new-api `ratio` 单位制 vs sub2api `rate_multiplier` 折扣制），不可直接比。
- 部分站**隐藏倍率**（new-api 关 `ExposeRatio`、sub2api simple 模式），仅靠抓取监控不到——需要真实成本探测。

## 架构

```
config ─▶ scheduler ─▶ adapter(newapi|sub2api) ─▶ normalize ─▶ store ─▶ dashboard
                                    │ ProbeCapabilities             │
                                    └──▶ probe(sk-key chat + usage delta) ─▶ changedet ─▶ alert ─▶ 钉钉/webhook
```

核心是 **Adapter 抽象 + 统一归一化到有效 USD/1M**（两类站原始契约见 `docs/upstream-contract.md`）。

## v1 范围

抓取→归一化→时序存储→仪表盘 · 变更检测+告警（钉钉/通用 webhook）· 跨站有效价对比矩阵 · **真实成本探测**（对账 markup）。不含 Prometheus exporter（v2）。

## 快速开始

```bash
# 1. 装 Go 1.22+（已装则跳过）
make build            # 产出 ./transitmonitor
make selftest         # 内置 E2E 自测（mock 站，无需真实站）

# 2. 配置（见下方示例）
export TRANSMONITOR_ENCRYPTION_KEY="$(openssl rand -hex 32)"

# 3. 一次性抓取（dry-run）
./transitmonitor -config config.yaml -once

# 4. 常驻 + 仪表盘（默认 localhost:7421）
./transitmonitor -config config.yaml
```

## Docker 部署（任意机器）

```bash
cp config.example.yaml config.yaml && $EDITOR config.yaml   # 填真实站凭据
# 用预构建多架构镜像（amd64+arm64，CI 在 v* tag 时推 ghcr.io）：
docker run -d -p 7421:7421 --name transitmonitor \
  -v "$PWD/config.yaml:/config/config.yaml:ro" -v transitmonitor-data:/data \
  -e TRANSMONITOR_DASHBOARD_TOKEN=secret \
  ghcr.io/yang-yang9/transitmonitor:v0.0.1
# 或本地构建：
# docker build -t transitmonitor:latest . && docker run -d -p 7421:7421 \
#   -v "$PWD/config.yaml:/config/config.yaml:ro" -v transitmonitor-data:/data \
#   -e TRANSMONITOR_DASHBOARD_TOKEN=secret transitmonitor:latest
# 或一键：TRANSMONITOR_DASHBOARD_TOKEN=secret docker compose up -d
curl -H "Authorization: Bearer secret" http://localhost:7421/api/stations
```
多架构镜像（amd64+arm64）：CI 在 `v*` tag 时自动 buildx push `ghcr.io/yang-yang9/transitmonitor:<tag>` + `:latest`。
⚠ 容器绑定 `0.0.0.0:7421`，**必须设 `TRANSMONITOR_DASHBOARD_TOKEN`** 才能外部访问（`/healthz` 免鉴权）。
完整部署手册见 [`docs/usage.md`](docs/usage.md)。

### 配置示例

```yaml
stations:
  - id: my-relay
    name: My Relay
    base_url: https://relay.example.com
    kind: newapi
    auth:
      pat: "<system access token>"      # 可选：抓 user/self/groups、option
      api_key: "sk-..."                  # /v1/* 与探测
      group: default
    poll_interval: 3m
    probe:
      enabled: false                    # 先 dry_run
      model: gpt-4o-mini
      max_input_tokens: 8
      max_output_tokens: 1
      max_cost_cents_per_run: 1
      dry_run: true
  - id: my-sub
    name: My Sub
    base_url: https://sub.example.com
    kind: sub2api
    auth:
      admin_api_key: "<admin api key>"   # 或 api_key + group（只读本 key 倍率）
      group: default
    poll_interval: 3m
    probe:
      enabled: false
      dry_run: true
```

## 文档导航

- 使用手册 / 部署：[`docs/usage.md`](docs/usage.md)
- 设计正文：[`docs/design.md`](docs/design.md)
- 上游契约速查（端点/鉴权/倍率，含 file:line）：[`docs/upstream-contract.md`](docs/upstream-contract.md)
- SDD 真源（能力规约，含 WHEN/THEN 场景）：[`openspec/changes/add-ratio-monitor-core/specs/`](openspec/changes/add-ratio-monitor-core/specs/)
- Claude 操作 skill：[`.claude/skills/transitmonitor/SKILL.md`](.claude/skills/transitmonitor/SKILL.md)
- 实施指引 / 任务 / 验证：[`openspec/changes/add-ratio-monitor-core/`](openspec/changes/add-ratio-monitor-core/)

## 开发（SDD + TDD）

```bash
make test          # go test ./...
make test-race     # -race
make vet fmt-check
make e2e           # 双 mock 站 E2E
```

每能力先写 `spec.md` 场景 → 翻译为表测试（RED）→ 实现（GREEN）→ 重构。见 `docs/design.md` §11。

## 安全注意

- 凭据 AES-GCM 静态加密，明文永不落库；日志/审计经 `secrets.Redact` 脱敏。
- 抓取只读 GET；**唯一写站动作 = 真实成本探测**（消耗真实 token/余额，受 `max_cost_cents_per_run` 等护栏约束、可干跑、可停、去重）。开启前先 `dry_run`。
- dashboard 非 localhost 要求 bearer token（`TRANSMONITOR_DASHBOARD_TOKEN`）。

## 许可

待定（TBD）。
