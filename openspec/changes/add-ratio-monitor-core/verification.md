# Verification: add-ratio-monitor-core

## 单元与适配器（Tier0–Tier4）
- [ ] `go test ./...` 全绿。
- [ ] `go test -race ./...` 全绿（并发安全：scheduler/poller/probe）。
- [ ] `go vet ./...` 无告警；`gofmt -l .` 无输出。
- [ ] normalize 表测试覆盖 `specs/normalization/spec.md` 全部 Scenario：new-api per-token/分组倍率/completion 缺失/fixed/37.5 哨兵开闭/sub2api multiplier/peak/simple/missing-base-price。
- [ ] changedet：值变/增删模型/哨兵翻转/幂等/fixed delta。
- [ ] probe 对账数学：new-api delta_quota→markup；sub2api delta_actual_cost→markup；零 delta/缺 base/fixed 边界。
- [ ] adapter：NewAPI（ratio_config 200、403→pricing 降级、pricing 空、status self_use on、user/self/groups、option、401）；Sub2API（billing+admin、billing 404 simple、user channels、model-pricing 缺失）。断言 CapabilityReport 标志 + 归一化行。
- [ ] store：迁移幂等；CRUD；retention 删旧 + 聚合；凭据加解密往返。
- [ ] dashboard：各路由 JSON/HTML 形状；矩阵形状；告警 CRUD。

## E2E（Tier5）
- [ ] `go test ./internal/e2e -run TestE2E -v`（或 `scripts/run-e2e-mock.sh`）通过。
- [ ] 断言：抓取→归一化→存储→翻转 mock 倍率后出现 ChangeEvent→内存 sink 收到钉钉形 + 通用 webhook 载荷→探测读 usage delta 对账 markup 在容差内。

## 真实站（手动，带护栏）
- [ ] new-api 一次性抓取：config `kind:newapi`+`pat`+`api_key:sk-...`+`probe.dry_run:true`，`go run ./cmd/transitmonitor -config config.yaml -once`；`GET /api/stations/<id>/health` 能力位、`GET /api/ratios?station=<id>` 归一化 USD/1M 行（非 self-use 无 37.5 行）。
- [ ] 真实探测：`probe.enabled:true,dry_run:false,max_cost_cents_per_run:1,max_input_tokens:8,max_output_tokens:1`，最便宜模型；`GET /api/probes?station=<id>&model=...` 有 measured/declared/markup/cost；`cost_usd≤护栏`；audit 有探测记录且无明文密钥。
- [ ] sub2api 真实站：`kind:subapi`+`admin_api_key`（或 `api_key`+`group`）；验 `/v1/sub2api/billing`；simple 模式则行标 `declared-unavailable (simple mode)` 且探测仍测 actual_cost。
- [ ] 仪表盘浏览：`/`、`/stations/<id>`、`/matrix?model=gpt-4o-mini`、`/changes`、`/probes`、`/alerts`。
- [ ] 保留：测试配短保留跑 downsample，旧 snapshots 删除、旧 ratio_observations 聚合。

## 安全核查
- [ ] `credentials.ciphertext` 非空、`credentials.nonce` 非空。
- [ ] `grep -ri "sk-" .` 在日志/`audit_log.detail` 中无明文 PAT/api-key（`secrets.Redact` 已包裹）。
- [ ] dashboard 默认 localhost-only 或 bearer token（`TRANSMONITOR_DASHBOARD_TOKEN`）。
