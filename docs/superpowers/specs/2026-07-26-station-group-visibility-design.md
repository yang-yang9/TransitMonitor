# 每站点分组展示配置（重点标记 + 部分隐藏）— 设计文档

- 日期：2026-07-26
- 状态：已与用户确认，待实现

## 1. 背景与目标

每个站点在轮询中会产生很多分组（分组 = group_name + 倍率，源自上游 API，无本地元数据）。概览页和矩阵页在分组很多时信息过载。本设计实现 **每站点、每分组** 的展示配置：勾选哪些分组展示、按相对优先级排序，从而在概览/矩阵页"部分隐藏"低优先分组、"重点标记"高优先分组。

**不改动**：变更 tab 里的 group-ratio 变更行永远展示（遵守项目原则"分组倍率是最核心数据，展示时永远不折叠/隐藏"）。

## 2. 用户语义

- "重点标记" = 勾选展示（visible=true）+ 排在前面（sort_order 小）。
- "部分隐藏" = visible=false 的分组不在概览/矩阵直接展示，但保留可找回入口（展开器）。
- 配置粒度：**每站点各一份**（不是全局统一）。每个站点独立勾选 + 排序。
- 入口形态：**可选择的行**（☑ 勾选 + ▲▼ 排序），放在站点设置区。

## 3. 数据模型

### 3.1 迁移 `internal/store/migrations/0005_station_group_config.sql`

```sql
CREATE TABLE IF NOT EXISTS station_group_config (
    station_id  TEXT    NOT NULL,
    group_name  TEXT    NOT NULL,
    visible     INTEGER NOT NULL DEFAULT 1,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (station_id, group_name),
    FOREIGN KEY (station_id) REFERENCES stations(id) ON DELETE CASCADE
);
```

沿用 `0004_station_sort_order.sql` 的迁移模式：放在 `migrations/` 下，由 `Store.Migrate`（`store.go:91-129`）按文件名顺序应用。`//go:embed migrations/*.sql`（`store.go:32`）会自动拾取。

### 3.2 Domain 结构体（`internal/domain/domain.go`）

```go
type StationGroupConfig struct {
    StationID string
    GroupName string
    Visible   bool
    SortOrder int
}
```

分组本身在 domain 里仍是 `string`（`RatioObservation.GroupName` `domain.go:245`、`RawSnapshot.GroupRatios` `domain.go:171`）——本设计不引入 `Group` 一等结构体，仅新增"每站点每分组的展示偏好"这张配置表。

### 3.3 默认值与未配置语义

- `visible` 默认 `1`（true）：未配置的新分组默认展示，绝不静默隐藏倍率。
- 配置行仅在用户显式勾选/排序时写入。
- `sort_order` 默认 `0`；空缺/重复序号由渲染排序兜底（见 4.1）。

## 4. 渲染辅助与默认行为

### 4.1 `GroupDisplay` 分区辅助（`internal/domain/domain.go` 或 dashboard 内 helper）

输入：某站点当前轮询到的分组（`map[group_name]ratio`，来自 `RawSnapshot.GroupRatios`）+ 该站点的配置行。输出有序的展示视图：

```go
type GroupDisplay struct {
    Name    string
    Ratio   float64
    Visible bool
    Order   int
}
```

排序键：`(visible desc, sort_order asc, name asc)`——对序号空缺/重复健壮；未配置的分组（无配置行）按 visible=true、sort_order=0、name 排序自然落到展示区尾部。

### 4.2 分组生命周期边界

- **新分组出现**（上游新增，无配置行）→ 默认 visible=true，排在展示区尾部，字母序。
- **隐藏的分组** → 不在概览/矩阵直接展示；概览卡提供 "另有 N 个已隐藏 ▾" 展开器，点开灰显，倍率数据可找回（不真消失）。
- **分组改名/消失**（上游改名）→ 旧配置行成孤儿（无害，当前数据无该组即不显示）；新名当作新分组默认展示。
- **站点删除** → `ON DELETE CASCADE` 清理配置。

## 5. Store API（`internal/store/store.go`，照 `ReorderStations` 模式）

```go
GetStationGroupConfigs(ctx, stationID) ([]domain.StationGroupConfig, error)
UpsertStationGroupConfig(ctx, cfg domain.StationGroupConfig) error
SaveStationGroupConfigs(ctx, stationID string, cfgs []domain.StationGroupConfig) error // 整体替换，last-write-wins
DeleteStationGroupConfig(ctx, stationID, groupName string) error
```

`SaveStationGroupConfigs` 用事务 `DELETE WHERE station_id=?` + 批量 insert 实现整体替换。

## 6. 配置入口（UI）

站点详情页 `/stations/{id}`（`dashboard_stations.go:137-353`）内嵌一个 `<details class="sec">` 区段 **"分组展示设置"**（与现有 `details.sec` 折叠区段风格一致，`ui.go:148-155`）。

- 列出该站点当前轮询到的全部分组（来自最新 `LatestGroupRatios`）+ 配置过但当前缺失的分组（孤儿）。
- 每行：☑ visible 复选框 + ▲▼ 上移/下移 + 组名 + 当前倍率（只读）+ 行内 AJAX 保存（fetch POST）。
- JS 风格沿用现有 `tmToggle*`（`ui.go`）；新增小块 JS 处理上下移与复选状态。
- 不新增路由，不污染现有站点编辑表单。

POST 端点：`POST /stations/{id}/groups` 接收整份列表（JSON：`[{group_name, visible, sort_order}]`），调 `SaveStationGroupConfigs`，返回 JSON `{ok:true}`。前端 AJAX 保存后局部刷新该区段，不整页跳转。

## 7. 概览页渲染（`internal/dashboard/dashboard.go:501-529`）

`groupRatioChart`（`ui.go:617-659`）当前画全部分组。改为：
- 传入 `GroupDisplay` 分区结果。
- 按 `sort_order` 顺序绘制（用户配置优先），着色仍用 b-cheap（<0.5）/b-warn（>1.0）/b-ok（其余）。
- 末尾追加 "另有 N 个已隐藏 ▾" 展开器（复用 `batch-toggle` / `tmToggleBatch`，`ui.go:396`），展开后灰显隐藏分组。
- 余额 badge、sparkline 不变。

## 8. 矩阵页渲染（`internal/dashboard/dashboard_pages.go:410-507` `matrixGroupTable`）

- **行存在规则**：某分组行只要 ≥1 个站点 visible=true 就显示（跨站点 OR，匹配"可选择的行"语义）。所有站点都隐藏的分组不出行 → 这就是矩阵页的"部分隐藏"。
- **单元格**：已显示行内，各站点单元格照常显示该站该组倍率（不做单元格级留空）。
- `matrixGroups`（`dashboard_pages.go:376-393`）改为只收集 visible 集合（OR）。
- 原有排序模式（ratio/name/cov，`dashboard_pages.go:454-471`）在可见集内仍生效。
- 行内对每个 visible=true 的站点单元格加 ★ 角标，便于看清该行的来源站点。

## 9. 站点详情页渲染（`dashboard_stations.go`）

- 详情页展示**全部**分组（全量数据，不隐藏），但按 `GroupDisplay` 排序（visible 在前）。
- visible 加 ★ 浅高亮；hidden 灰显带 "已隐藏" 标签。
- `groupRatioChart`（hero）与 per-group trend sparklines（`dashboard_stations.go:259-303`）按配置排序。
- **变更 tab 完全不动**：group-ratio 变更行（`tr.ratio-row`，`dashboard_pages.go:668-669,717-721`）永远展示，不折叠不隐藏。

## 10. i18n（`internal/dashboard/i18n.go`，zh/en 各加）

| key | zh | en |
|---|---|---|
| `section.groupsettings` | 分组展示设置 | Group display settings |
| `btn.savegroupconfig` | 保存分组设置 | Save group settings |
| `badge.hidden` | 已隐藏 | hidden |
| `batch.hiddenmore` | 另有 %d 个已隐藏 | %d hidden |
| `col.visible` | 展示 | Show |
| `col.order` | 顺序 | Order |
| `btn.moveup` | 上移 | Move up |
| `btn.movedown` | 下移 | Move down |

`batch.hiddenmore` 用 `fmt.Sprintf` 注入数量（参考现有 `batch.showmore` "展开 %d 条更多" 的处理）。

## 11. 测试

- **Store**：`SaveStationGroupConfigs` 往返（写后读一致）；站点删除级联清空；未配置的默认 visible=true。
- **GroupDisplay 排序**：visible/hidden/未配置混合；序号空缺与重复。
- **Dashboard**：概览卡只画 visible + 展开器内容；矩阵行 OR-of-visible；详情页全量但有序 + 高亮/灰显。
- **gofmt**：用 `/home/admin/.local/go/bin/go`（1.25.0）格式化；系统 go 1.23.4 不达标。goffmt 是 CI 老红点。

## 12. 不做的事（YAGNI）

- 不引入 `Group` 一等结构体；分组仍是字符串 + 倍率。
- 不做全局分组清单；配置粒度严格每站点。
- 不做单元格级隐藏；矩阵行级 OR 即可。
- 不动变更 tab 的倍率变更行展示逻辑。
- 不做"重点"独立的视觉强调系统；visible + sort_order 即表达重点，详情页 ★ 为轻量点缀。
