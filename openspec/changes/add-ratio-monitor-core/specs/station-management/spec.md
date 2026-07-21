## ADDED Requirements

### Requirement: 站点必须经 YAML 配置登记并支持启停

系统 SHALL 通过 YAML 配置文件登记监控目标站，每站 MUST 含 `id`（唯一）、`name`、`base_url`、`kind`（`newapi`|`sub2api`）、`auth`、`poll_interval`、可选 `probe` 与 `tags`，并 MUST 支持 `enabled` 开关。`enabled=false` 的站 MUST NOT 被调度器抓取或探测。

#### Scenario: 登记一个 new-api 站

- **WHEN** 配置文件含一个 `kind=newapi`、`base_url=https://relay.example.com`、`auth.pat` 与 `auth.api_key` 的站
- **THEN** 系统 MUST 持久化该站并使其进入调度
- **THEN** `GET /api/stations` MUST 返回该站（凭据字段 MUST 脱敏）

#### Scenario: 停用的站不调度

- **WHEN** 某站 `enabled=false`
- **THEN** 调度器 MUST NOT 为该站发起任何抓取或探测请求

#### Scenario: id 唯一性

- **WHEN** 配置或 CRUD 提交一个已存在的 `id`
- **THEN** 系统 MUST 拒绝并返回明确错误，MUST NOT 覆盖既有站

### Requirement: 凭据必须静态加密存储且明文永不落库

系统 MUST 用 AES-GCM 对凭据（`pat`、`api_key`、`admin_api_key`、`admin_pass`、`jwt`、webhook secret 等）静态加密，密钥来自环境变量 `TRANSMONITOR_ENCRYPTION_KEY`。MUST 只在 `credentials` 表存 `ciphertext` 与 `nonce`，MUST NOT 将明文凭据写入 `stations` 表或任何日志/审计明细。

#### Scenario: 凭据加密往返

- **WHEN** 登记一个含 `api_key=sk-abc123` 的站并随后读取
- **THEN** `credentials.ciphertext` MUST 非空且与明文不同
- **THEN** 解密后 MUST 还原 `sk-abc123`
- **THEN** `stations` 表 MUST NOT 含 `sk-abc123` 明文

#### Scenario: 缺少加密密钥时拒绝启动

- **WHEN** 启动时 `TRANSMONITOR_ENCRYPTION_KEY` 未设置或为空
- **THEN** 系统 MUST 拒绝启动并报明确错误（当存在任何需加密的凭据时）

### Requirement: 日志与审计必须脱敏所有凭据字段

系统 MUST 对所有被记入日志或 `audit_log.detail` 的凭据字段经 `secrets.Redact` 处理，输出形如 `sk-***` / `pat-***` / `***` 的掩码，MUST NOT 出现完整 `sk-`/PAT 字符串。

#### Scenario: 探测失败日志不含明文 key

- **WHEN** 探测请求因鉴权失败返回 401，系统记录错误日志
- **THEN** 日志中的 `api_key` MUST 显示为脱敏掩码
- **THEN** `grep "sk-abc123"` 全仓日志与 audit MUST 无命中
