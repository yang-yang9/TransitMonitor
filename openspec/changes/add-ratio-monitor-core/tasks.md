# Tasks: add-ratio-monitor-core

> 状态：`[x]` 完成 / `[~]` 进行中 / `[ ]` 待办。

## M1. 文档落盘（先于代码）
- [x] openspec/config.yaml、.openspec.yaml、proposal.md、design.md
- [x] tasks.md、implementation-guide.md、verification.md
- [x] specs/<10 能力>/spec.md（normalization 为完整样例）
- [x] docs/design.md、docs/upstream-contract.md
- [x] README.md、Makefile、.gitignore、config.example.yaml

## M2. 工程骨架
- [x] 安装 Go 1.23.4（后台 + profile PATH）
- [x] git init；go mod init transitmonitor；go get 依赖（modernc/sqlite、yaml.v3、chi/v5、x/time）
- [x] cmd/transitmonitor/main.go（flags: -config/-once/-selftest/-addr + 信号处理 + 全接线）；internal/ 包目录

## M3. Tier0 纯函数
- [x] internal/normalize + 表测试 ✅
- [x] internal/changedet + 表测试 ✅
- [x] internal/probe 对账纯函数 + 表测试 ✅

## M4. Tier1 adapter + httptest
- [x] internal/adapter/adapter.go（接口 + CapabilityReport + NewAdapter 工厂）✅
- [x] internal/adapter/newapi + fixtures ✅
- [x] internal/adapter/sub2api + fixtures ✅

## M5. Tier2 store + secrets
- [x] internal/secrets AES-GCM + Redact ✅
- [x] internal/store schema + embed migrations（幂等）+ 索引 ✅
- [x] CRUD ratio_observations/snapshots/change_events/probe_results + LatestRatioObservations/ListChangeEvents/ListProbeResults ✅
- [x] 凭据加解密（credentials 表无明文列）✅
- [x] retention/downsample（删旧 snapshots + 小时聚合 + 幂等）✅

## M6. Tier3 scheduler ✅
- [x] internal/scheduler 每站 poller + jitter + 退避 + 优雅关停（Run/PollOnce）✅
- [x] PollOnce: probe→fetch→store→changedet.Diff→alert.Evaluate→dispatch ✅
- [x] 探测调度（更稀疏）+ (station,model) 去重（Prober 内置 10min 窗口）✅

## M7. Tier4 dashboard + alert
- [x] internal/alert 规则评估 + 钉钉签名 + 通用 webhook + 内存 sink ✅
- [x] internal/dashboard（chi + html/template）路由 + 页面 + 鉴权 + 凭据脱敏 ✅
- [x] internal/changedet 接线 ✅

## M8. 探测编排 + 矩阵 + E2E
- [x] internal/probe 编排（发真实 chat + 读 usage delta + 调 Reconcile* + 成本护栏/去重/干跑）✅
- [x] scheduler 接线 Prober（启用时每轮抓取后探测 + 存 probe_results + 告警）✅
- [x] /api/matrix 跨站矩阵（model × station 有效 USD/1M；sentinel 标签）✅
- [x] internal/e2e 双 mock 站（new-api 形 / sub2api 形）翻转倍率 → ChangeEvent + 告警 sink + 探测对账 markup ✅

## M9. 收尾
- [x] main.go 全接线 + -selftest/-version 自测 + env 覆盖 + 优雅关停 + slog ✅
- [x] config.example.yaml 示例配置（可加载、Duration 解析）✅
- [x] audit_log 写入器 + ListAuditLogs + 接线（startup / probe.run / credentials.persisted）+ /api/audit ✅
- [x] /healthz（免鉴权，供 Docker/k8s healthcheck）✅
- [x] 每日保留任务（scheduler.retentionLoop）✅
- [x] 凭据静态加密（TRANSMONITOR_ENCRYPTION_KEY → AES-GCM 入库）✅
- [x] CI workflow（.github/workflows/ci.yml：gofmt/vet/test-race/build/selftest）✅
- [x] Docker：Dockerfile（多阶段、无 CGO、多架构）+ .dockerignore + docker-compose.yml + make docker* ✅
- [x] 使用手册 docs/usage.md + Claude skill .claude/skills/transitmonitor/SKILL.md ✅
- [x] README Docker 章节 + 文档导航 ✅

## 当前测试状态
`go test ./...` + `go test -race ./...` + `go vet ./...` + `go build ./...` + `gofmt -l .` 全部清洁（12 包绿）。
`./transitmonitor -selftest` 通过（双 mock 站全链路 + 探测对账 markup=0.00%）。
`./transitmonitor -config config.example.yaml` 启动 dashboard + 调度 + 保留，`curl /healthz`→200、`curl /api/audit`→startup 条目，fake-URL 优雅降级无崩溃，slog JSON 日志，Ctrl-C 优雅关停。
Docker：本机未装 docker，Dockerfile/compose 为标准多阶段多架构（无 CGO），在任意装了 docker 的机器 `docker build` 即可。
