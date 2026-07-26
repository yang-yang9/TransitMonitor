# 每站点分组展示配置（重点标记 + 部分隐藏）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现每站点、每分组的展示配置（visible + sort_order），让概览页/矩阵页"部分隐藏"低优先分组、"重点标记"高优先分组，倍率数据通过展开器始终可找回。

**Architecture:** 新增 `station_group_config` 表（迁移 0005）持久化每站点每分组的 `visible`/`sort_order`；domain 层加 `PartitionGroups` 纯函数把当前轮询分组与配置合并成有序展示视图；dashboard 层在概览卡按 visible 切片绘制 + 隐藏展开器，矩阵页按跨站 OR-of-visible 过滤行，详情页内嵌"分组展示设置"区段 + `POST /stations/{id}/groups` 保存端点。变更 tab 的倍率变更行完全不动。

**Tech Stack:** Go 1.25（用 `/home/admin/.local/go/bin/go`，系统 go 1.23.4 不达标）、modernc.org/sqlite、go-chi/chi v5、纯 HTML/CSS/JS（无前端框架）、embedded SQL migrations。

**参考 spec:** `docs/superpowers/specs/2026-07-26-station-group-visibility-design.md`

---

## File Structure

| 文件 | 责任 | 动作 |
|---|---|---|
| `internal/store/migrations/0005_station_group_config.sql` | 新表 DDL | Create |
| `internal/domain/domain.go` | `StationGroupConfig` / `GroupDisplay` 结构体 + `PartitionGroups` / `SplitVisible` 纯函数 | Modify（加 `sort` import + 末尾追加） |
| `internal/domain/domain_group_test.go` | `PartitionGroups` / `SplitVisible` 单测 | Create |
| `internal/store/store.go` | 4 个 CRUD 方法（照 `ReorderStations` 模式） | Modify（追加在 `ReorderStations` 之后） |
| `internal/store/store_station_group_test.go` | Store 往返/级联/默认值单测 | Create |
| `internal/dashboard/i18n.go` | zh/en 新 key | Modify |
| `internal/dashboard/ui.go` | `groupRatioChart` 改签名 + 隐藏展开器 + CSS | Modify |
| `internal/dashboard/dashboard.go` | 概览卡分区渲染 + 注册 `POST /stations/{id}/groups` 路由 | Modify |
| `internal/dashboard/dashboard_stations.go` | 详情页"分组展示设置"区段 + 保存端点 + 详情页排序 | Modify |
| `internal/dashboard/dashboard_pages.go` | `matrixGroupTable` / `matrixGroups` OR-of-visible | Modify |
| `internal/dashboard/dashboard_group_config_test.go` | 概览/矩阵/详情/保存端点渲染测 | Create |

**约定：** 仓库有自动提交钩子（每次编辑后会 amend 进最近一次提交），所以 `git diff` 刚编辑后常为空——验证用 `git show HEAD:<file>` 或直接跑测试。每个 Task 末尾的 commit 步骤是"尽力而为"：若钩子已自动提交，`git diff --staged` 为空属正常，跳过即可。提交署名 `Devix <devix@transitmonitor.dev>`，不加 `Co-Authored-By: Claude` 尾注。

---

### Task 1: 迁移 0005 + domain 结构体

**Files:**
- Create: `internal/store/migrations/0005_station_group_config.sql`
- Modify: `internal/domain/domain.go`（import 块 7-11 行；末尾追加结构体，文件末行 ~310 之后）

- [ ] **Step 1: 写迁移文件**

`internal/store/migrations/0005_station_group_config.sql`:
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

- [ ] **Step 2: 给 domain.go 加 `sort` import**

`internal/domain/domain.go` 第 7-11 行 import 块改为：
```go
import (
	"errors"
	"fmt"
	"sort"
	"time"
)
```

- [ ] **Step 3: 在 domain.go 末尾追加结构体**

追加到 `internal/domain/domain.go` 文件末尾：
```go

// StationGroupConfig is a per-station, per-group display preference persisted in
// station_group_config. A group with no row defaults to visible=true, sort_order=0
// (see PartitionGroups) so newly-polled groups never silently disappear.
type StationGroupConfig struct {
	StationID string
	GroupName string
	Visible   bool
	SortOrder int
}

// GroupDisplay is a group merged with its display config, ordered for rendering.
type GroupDisplay struct {
	Name    string
	Ratio   float64
	Visible bool
	Order   int
}

// PartitionGroups merges a station's current group ratios with its display config
// into an ordered view: visible first (by sort_order asc, then name asc), then
// hidden (same tie-break). Groups with no config row default to visible=true,
// sort_order=0 — so new groups surface by default rather than silently hiding.
func PartitionGroups(ratios map[string]float64, cfgs []StationGroupConfig) []GroupDisplay {
	byName := make(map[string]StationGroupConfig, len(cfgs))
	for _, c := range cfgs {
		byName[c.GroupName] = c
	}
	out := make([]GroupDisplay, 0, len(ratios))
	for name, r := range ratios {
		c, ok := byName[name]
		if !ok {
			c = StationGroupConfig{Visible: true, SortOrder: 0}
		}
		out = append(out, GroupDisplay{Name: name, Ratio: r, Visible: c.Visible, Order: c.SortOrder})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Visible != out[j].Visible {
			return out[i].Visible
		}
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// SplitVisible partitions an ordered GroupDisplay slice into (visible, hidden),
// preserving order within each half.
func SplitVisible(groups []GroupDisplay) (visible, hidden []GroupDisplay) {
	for _, g := range groups {
		if g.Visible {
			visible = append(visible, g)
		} else {
			hidden = append(hidden, g)
		}
	}
	return visible, hidden
}
```

- [ ] **Step 4: 编译确认**

Run: `/home/admin/.local/go/bin/go build ./internal/domain/`
Expected: 无输出（成功）

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/0005_station_group_config.sql internal/domain/domain.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(domain): 加 station_group_config 迁移 + GroupDisplay 分区辅助"
```

---

### Task 2: PartitionGroups / SplitVisible 单测

**Files:**
- Test: `internal/domain/domain_group_test.go`

- [ ] **Step 1: 写测试**

`internal/domain/domain_group_test.go`:
```go
package domain

import (
	"reflect"
	"testing"
)

func TestPartitionGroups(t *testing.T) {
	ratios := map[string]float64{
		"vip": 0.5, "svip": 0.8, "pro": 1.0, "default": 1.0, "trial": 1.5, "internal": 2.0,
	}
	cfgs := []StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "svip", Visible: true, SortOrder: 1},
		{StationID: "s1", GroupName: "pro", Visible: false, SortOrder: 0},
		{StationID: "s1", GroupName: "trial", Visible: false, SortOrder: 1},
	}
	got := PartitionGroups(ratios, cfgs)

	// visible block first (by sort_order), then hidden block (by sort_order),
	// unconfigured (default, internal) default to visible and land among the
	// visible block by sort_order=0 then name.
	want := []GroupDisplay{
		{Name: "default", Ratio: 1.0, Visible: true, Order: 0}, // unconfigured, sort 0, name before vip
		{Name: "internal", Ratio: 2.0, Visible: true, Order: 0},
		{Name: "vip", Ratio: 0.5, Visible: true, Order: 0},
		{Name: "svip", Ratio: 0.8, Visible: true, Order: 1},
		{Name: "pro", Ratio: 1.0, Visible: false, Order: 0},
		{Name: "trial", Ratio: 1.5, Visible: false, Order: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PartitionGroups order wrong:\n got=%+v\nwant=%+v", got, want)
	}

	vis, hid := SplitVisible(got)
	if len(vis) != 4 || len(hid) != 2 {
		t.Fatalf("split: want 4 vis / 2 hid, got %d/%d", len(vis), len(hid))
	}
	if hid[0].Name != "pro" || hid[1].Name != "trial" {
		t.Errorf("hidden order wrong: %+v", hid)
	}
}

func TestPartitionGroupsEmptyConfigDefaultsVisible(t *testing.T) {
	ratios := map[string]float64{"a": 1.0, "b": 2.0}
	got := PartitionGroups(ratios, nil)
	for _, g := range got {
		if !g.Visible {
			t.Errorf("group %s: unconfigured should default visible=true", g.Name)
		}
	}
	// no config → sort_order 0 for all → stable name asc
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("name order wrong: %+v", got)
	}
}

func TestPartitionGroupsDuplicateSortOrderStableByName(t *testing.T) {
	ratios := map[string]float64{"b": 1.0, "a": 1.0, "c": 1.0}
	cfgs := []StationGroupConfig{
		{GroupName: "a", Visible: true, SortOrder: 5},
		{GroupName: "b", Visible: true, SortOrder: 5},
		{GroupName: "c", Visible: true, SortOrder: 5},
	}
	got := PartitionGroups(ratios, cfgs)
	if got[0].Name != "a" || got[1].Name != "b" || got[2].Name != "c" {
		t.Errorf("duplicate sort_order should tie-break by name: %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认通过**

Run: `/home/admin/.local/go/bin/go test ./internal/domain/ -run 'TestPartitionGroups|TestSplitVisible' -v`
Expected: PASS（3 个测试）

- [ ] **Step 3: Commit**

```bash
git add internal/domain/domain_group_test.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "test(domain): PartitionGroups 排序/默认/重复序号"
```

---

### Task 3: Store CRUD

**Files:**
- Modify: `internal/store/store.go`（追加在 `ReorderStations` 之后，~455 行之后）
- Test: `internal/store/store_station_group_test.go`

- [ ] **Step 1: 写失败的测试**

`internal/store/store_station_group_test.go`:
```go
package store

import (
	"context"
	"testing"

	"transitmonitor/internal/domain"
)

func TestStationGroupConfigCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertStation(t, "s1")

	// initial: no config rows
	if got, err := s.GetStationGroupConfigs(ctx, "s1"); err != nil || len(got) != 0 {
		t.Fatalf("initial get: got=%v err=%v", got, err)
	}

	// save two visible + one hidden, with explicit ordering
	if err := s.SaveStationGroupConfigs(ctx, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "svip", Visible: true, SortOrder: 1},
		{StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.GetStationGroupConfigs(ctx, "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows got %d", len(got))
	}
	byName := map[string]domain.StationGroupConfig{}
	for _, c := range got {
		byName[c.GroupName] = c
	}
	if c := byName["vip"]; !c.Visible || c.SortOrder != 0 {
		t.Errorf("vip row wrong: %+v", c)
	}
	if c := byName["internal"]; c.Visible || c.SortOrder != 0 {
		t.Errorf("internal should be hidden: %+v", c)
	}

	// replace-all: saving a different set drops the old rows
	if err := s.SaveStationGroupConfigs(ctx, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "pro", Visible: false, SortOrder: 0},
	}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got2, _ := s.GetStationGroupConfigs(ctx, "s1")
	if len(got2) != 1 || got2[0].GroupName != "pro" {
		t.Errorf("replace-all failed: %+v", got2)
	}
}

func TestStationGroupConfigCascadeDelete(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertStation(t, "s1")
	_ = s.SaveStationGroupConfigs(ctx, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
	})
	if err := s.DeleteStation(ctx, "s1"); err != nil {
		t.Fatalf("delete station: %v", err)
	}
	got, _ := s.GetStationGroupConfigs(ctx, "s1")
	if len(got) != 0 {
		t.Errorf("cascade failed: still %d rows", len(got))
	}
}

func TestUpsertStationGroupConfig(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertStation(t, "s1")
	// upsert then toggle
	_ = s.UpsertStationGroupConfig(ctx, domain.StationGroupConfig{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0})
	_ = s.UpsertStationGroupConfig(ctx, domain.StationGroupConfig{StationID: "s1", GroupName: "vip", Visible: false, SortOrder: 2})
	got, _ := s.GetStationGroupConfigs(ctx, "s1")
	if len(got) != 1 || got[0].Visible || got[0].SortOrder != 2 {
		t.Errorf("upsert did not update in place: %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/home/admin/.local/go/bin/go test ./internal/store/ -run TestStationGroupConfig -v`
Expected: FAIL —— `s.GetStationGroupConfigs undefined` / `s.SaveStationGroupConfigs undefined`

- [ ] **Step 3: 写实现**

追加到 `internal/store/store.go`（`ReorderStations` 函数之后，`InsertSnapshot` 之前，约 456 行）：
```go

// GetStationGroupConfigs returns all per-group display config rows for a station.
// Groups with no row are absent — callers treat absence as visible=true,sort_order=0
// (see domain.PartitionGroups).
func (s *Store) GetStationGroupConfigs(ctx context.Context, stationID string) ([]domain.StationGroupConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT station_id, group_name, visible, sort_order FROM station_group_config WHERE station_id=? ORDER BY sort_order, group_name`,
		stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StationGroupConfig
	for rows.Next() {
		var c domain.StationGroupConfig
		var vis int
		if err := rows.Scan(&c.StationID, &c.GroupName, &vis, &c.SortOrder); err != nil {
			return nil, err
		}
		c.Visible = vis != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertStationGroupConfig inserts or updates a single group's display config.
func (s *Store) UpsertStationGroupConfig(ctx context.Context, cfg domain.StationGroupConfig) error {
	vis := 0
	if cfg.Visible {
		vis = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO station_group_config (station_id, group_name, visible, sort_order) VALUES (?,?,?,?)
		 ON CONFLICT(station_id, group_name) DO UPDATE SET visible=excluded.visible, sort_order=excluded.sort_order`,
		cfg.StationID, cfg.GroupName, vis, cfg.SortOrder)
	return err
}

// SaveStationGroupConfigs replaces the entire per-station config set in one
// transaction (delete-then-insert). Last-write-wins; an empty cfgs slice clears
// all rows for the station.
func (s *Store) SaveStationGroupConfigs(ctx context.Context, stationID string, cfgs []domain.StationGroupConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM station_group_config WHERE station_id=?`, stationID); err != nil {
		return err
	}
	for _, c := range cfgs {
		vis := 0
		if c.Visible {
			vis = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO station_group_config (station_id, group_name, visible, sort_order) VALUES (?,?,?,?)`,
			c.StationID, c.GroupName, vis, c.SortOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteStationGroupConfig removes one group's config row (optional cleanup;
// orphaned rows are harmless since rendering only reads current-poll groups).
func (s *Store) DeleteStationGroupConfig(ctx context.Context, stationID, groupName string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM station_group_config WHERE station_id=? AND group_name=?`, stationID, groupName)
	return err
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `/home/admin/.local/go/bin/go test ./internal/store/ -run TestStationGroupConfig -v`
Expected: PASS（3 个测试）

- [ ] **Step 5: 跑全 store 测试确认无回归**

Run: `/home/admin/.local/go/bin/go test ./internal/store/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_station_group_test.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(store): station_group_config CRUD（往返/级联/单行 upsert）"
```

---

### Task 4: i18n 新 key

**Files:**
- Modify: `internal/dashboard/i18n.go`（zh 块 + en 块各加一批 key）

- [ ] **Step 1: 在 zh 块追加 key**

在 `internal/dashboard/i18n.go` 的 `"zh"` map 内，紧挨 `"expand.modelchanges": "展开模型变更",` 那一行之后插入（约 54 行附近，zh 块内）：
```go
		"section.groupsettings": "分组展示设置",
		"btn.savegroupconfig":    "保存分组设置",
		"badge.hidden":           "已隐藏",
		"batch.hiddenmore":       "另有 %d 个已隐藏 ▾",
		"col.visible":            "展示",
		"col.order":             "顺序",
		"btn.moveup":             "上移",
		"btn.movedown":           "下移",
```

- [ ] **Step 2: 在 en 块追加对应 key**

找到 en 块里对应的 `"expand.modelchanges":` 行（en 块结构镜像 zh），紧挨其后插入：
```go
		"section.groupsettings": "Group display settings",
		"btn.savegroupconfig":    "Save group settings",
		"badge.hidden":           "hidden",
		"batch.hiddenmore":       "%d hidden ▾",
		"col.visible":            "Show",
		"col.order":              "Order",
		"btn.moveup":             "Move up",
		"btn.movedown":           "Move down",
```

- [ ] **Step 3: 编译确认**

Run: `/home/admin/.local/go/bin/go build ./internal/dashboard/`
Expected: 无输出（成功）

- [ ] **Step 4: Commit**

```bash
git add internal/dashboard/i18n.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(i18n): 分组展示设置 zh/en 文案"
```

---

### Task 5: groupRatioChart 改签名 + 隐藏展开器 + 概览卡分区渲染

**Files:**
- Modify: `internal/dashboard/ui.go`（`groupRatioChart` ~632-674；CSS ~127 行后）
- Modify: `internal/dashboard/dashboard.go`（概览卡 ~501-502；路由 ~105 行）
- Test: `internal/dashboard/dashboard_group_config_test.go`（本 Task 只加第一个测试）

- [ ] **Step 1: 写失败的测试（概览卡隐藏展开器）**

`internal/dashboard/dashboard_group_config_test.go`:
```go
package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

// helper: save a station's group config (visible + hidden) directly via the store.
func saveGroupCfg(t *testing.T, srv *Server, stationID string, cfgs []domain.StationGroupConfig) {
	t.Helper()
	if err := srv.store.SaveStationGroupConfigs(context.Background(), stationID, cfgs); err != nil {
		t.Fatalf("save group cfg: %v", err)
	}
}

func TestOverviewHidesConfiguredHiddenGroups(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
		StationID: "s1", ObservedAt: now,
		GroupRatios: map[string]float64{"vip": 0.5, "internal": 2.0},
	})
	saveGroupCfg(t, srv, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0},
	})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "vip") || !strings.Contains(body, "0.50x") {
		t.Errorf("visible group vip should be rendered:\n%s", body)
	}
	if !strings.Contains(body, "已隐藏") {
		t.Errorf("hidden expander marker missing:\n%s", body)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestOverviewHidesConfiguredHiddenGroups -v`
Expected: FAIL —— `已隐藏` 不在 body 中（groupRatioChart 还画了全部分组）

- [ ] **Step 3: 改 groupRatioChart 签名为 `[]GroupDisplay`**

把 `internal/dashboard/ui.go` 中 `groupRatioChart`（632-674 行）整个函数替换为：
```go
// groupRatioChart renders the horizontal bar chart of group ratios in the given
// order (caller partitions + orders via domain.PartitionGroups). lg=true renders
// the larger hero variant (station detail); false the compact variant (overview
// cards). Color is by ratio value: b-cheap (<0.5), b-warn (>1.0), else b-ok.
// Returns "" when the slice is empty.
func groupRatioChart(groups []domain.GroupDisplay, lg bool) string {
	if len(groups) == 0 {
		return ""
	}
	maxV := 0.0
	for _, g := range groups {
		if g.Ratio > maxV {
			maxV = g.Ratio
		}
	}
	cls := ""
	if lg {
		cls = " lg"
	}
	var b strings.Builder
	b.WriteString(`<div class="gr-chart` + cls + `">`)
	for _, g := range groups {
		pct := 0.0
		if maxV > 0 {
			pct = g.Ratio / maxV * 100
		}
		bc := "b-ok"
		if g.Ratio < 0.5 {
			bc = "b-cheap"
		} else if g.Ratio > 1.0 {
			bc = "b-warn"
		}
		b.WriteString(fmt.Sprintf(
			`<div class="gr-row"><span class="gr-name" title="%s">%s</span>`+
				`<div class="gr-track"><span class="gr-bar %s" style="width:%.1f%%"></span></div>`+
				`<span class="gr-val">%.2fx</span></div>`,
			esc(g.Name), esc(g.Name), bc, pct, g.Ratio))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// renderHiddenGroupsExpander renders the "+N hidden" collapsible footer for a
// station card, listing hidden groups dimmed so their ratios stay reachable
// (ratios are never truly hidden — honoring the project principle).
func renderHiddenGroupsExpander(lang string, hidden []domain.GroupDisplay) string {
	if len(hidden) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<details class="gr-hidden"><summary>`)
	b.WriteString(fmt.Sprintf(t(lang, "batch.hiddenmore"), len(hidden)))
	b.WriteString(`</summary><div class="gr-chart">`)
	for _, g := range hidden {
		bc := "b-ok"
		if g.Ratio < 0.5 {
			bc = "b-cheap"
		} else if g.Ratio > 1.0 {
			bc = "b-warn"
		}
		b.WriteString(fmt.Sprintf(
			`<div class="gr-row gr-dim"><span class="gr-name" title="%s">%s</span>`+
				`<div class="gr-track"><span class="gr-bar %s" style="width:100%%"></span></div>`+
				`<span class="gr-val">%.2fx</span></div>`,
			esc(g.Name), esc(g.Name), bc, g.Ratio))
	}
	b.WriteString(`</div></details>`)
	return b.String()
}
```

- [ ] **Step 4: 加 CSS（隐藏展开器 + dim）**

在 `internal/dashboard/ui.go` 的 `appCSS` 常量内，紧挨 `.gr-val{...}` 那一行（127 行）之后插入：
```css
.gr-hidden{margin:.4rem 0 0;border:1px dashed var(--border);border-radius:var(--radius-xs);background:var(--bg-1)}
.gr-hidden>summary{cursor:pointer;padding:.35rem .6rem;font-size:.78rem;color:var(--muted);list-style:none}
.gr-hidden>summary::before{content:"▸ ";color:var(--muted)}
.gr-hidden[open]>summary::before{content:"▾ "}
.gr-dim{opacity:.5}
.gr-dim .gr-track{background:transparent}
```

- [ ] **Step 5: 修概览卡调用点**

`internal/dashboard/dashboard.go` 概览卡循环内（501-502 行 `grs, _ := ...` / `chart := groupRatioChart(grs, false)`）替换为：
```go
			grs, _ := s.store.LatestGroupRatios(ctx, st.ID)
			cfgs, _ := s.store.GetStationGroupConfigs(ctx, st.ID)
			visible, hidden := domain.SplitVisible(domain.PartitionGroups(grs, cfgs))
			chart := groupRatioChart(visible, false) + renderHiddenGroupsExpander(lang, hidden)
```

- [ ] **Step 6: 修详情页 hero 调用点（签名变了，否则编译不过）**

`internal/dashboard/dashboard_stations.go` 228 行 `heroChart := groupRatioChart(groupRatios, true)` 暂时替换为（详情页排序在 Task 8 精修，这里先让编译通过、按配置排序）：
```go
	stGroupCfgs, _ := s.store.GetStationGroupConfigs(ctx, id)
	heroChart := groupRatioChart(domain.PartitionGroups(groupRatios, stGroupCfgs), true)
```

- [ ] **Step 7: 跑测试确认通过**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestOverviewHidesConfiguredHiddenGroups -v`
Expected: PASS

- [ ] **Step 8: 跑全 dashboard 测试确认无回归（groupRatioChart 签名变更可能影响其他渲染测试）**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/`
Expected: PASS（若 TestMatrixGroupHTML 等失败，检查是否依赖 groupRatioChart 旧签名——矩阵不走 groupRatioChart，应不受影响）

- [ ] **Step 9: Commit**

```bash
git add internal/dashboard/ui.go internal/dashboard/dashboard.go internal/dashboard/dashboard_stations.go internal/dashboard/dashboard_group_config_test.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(dashboard): 概览卡按 visible 分区 + 隐藏展开器，groupRatioChart 改签名"
```

---

### Task 6: 详情页"分组展示设置"区段 + POST 保存端点

**Files:**
- Modify: `internal/dashboard/dashboard_stations.go`（详情页 body 末尾加区段 ~351 行；新增 handler + 路由）
- Modify: `internal/dashboard/dashboard.go`（路由 ~105 行）
- Test: `internal/dashboard/dashboard_group_config_test.go`（追加测试）

- [ ] **Step 1: 写失败的测试（保存端点 + 区段渲染）**

追加到 `internal/dashboard/dashboard_group_config_test.go` 末尾：
```go
func TestStationGroupSettingsSaveAndRender(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
		StationID: "s1", ObservedAt: now,
		GroupRatios: map[string]float64{"vip": 0.5, "pro": 1.0, "internal": 2.0},
	})

	// POST the full config: vip visible(0), pro hidden(0), internal visible(1)
	body := `{"groups":[{"group_name":"vip","visible":true,"sort_order":0},` +
		`{"group_name":"pro","visible":false,"sort_order":0},` +
		`{"group_name":"internal","visible":true,"sort_order":1}]}`
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodPost, "/stations/s1/groups", strings.NewReader(body)))
	if r.Code != 200 {
		t.Fatalf("save: want 200 got %d: %s", r.Code, r.Body.String())
	}

	// persisted?
	got, _ := st.GetStationGroupConfigs(ctx, "s1")
	if len(got) != 3 {
		t.Fatalf("want 3 cfg rows got %d", len(got))
	}
	byName := map[string]domain.StationGroupConfig{}
	for _, c := range got {
		byName[c.GroupName] = c
	}
	if byName["pro"].Visible {
		t.Error("pro should be hidden after save")
	}
	if byName["internal"].SortOrder != 1 {
		t.Errorf("internal sort_order: want 1 got %d", byName["internal"].SortOrder)
	}

	// detail page renders the settings section with a checkbox per group
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/stations/s1"))
	if r2.Code != 200 {
		t.Fatalf("detail: want 200 got %d", r2.Code)
	}
	html := r2.Body.String()
	if !strings.Contains(html, "分组展示设置") {
		t.Errorf("settings section missing:\n%s", html)
	}
	if !strings.Contains(html, `name="visible"`) || !strings.Contains(html, "pro") {
		t.Errorf("per-group checkbox row missing:\n%s", html)
	}
}
```

注意：`localReq` 目前只接收 `(method, target)`——Step 1 还需给 `localReq` 加 body 入参。但 `localReq` 在 `dashboard_test.go` 被多处调用为双参。**不要改 `localReq` 签名**（会破坏既有调用）。改为在本测试内直接构造 request：
把上面测试里的两处 `localReq(http.Method..., "/...", strings.NewReader(...))` 换成直接构造：

把 `srv.Handler().ServeHTTP(r, localReq(http.MethodPost, "/stations/s1/groups", strings.NewReader(body)))` 替换为：
```go
	req := httptest.NewRequest(http.MethodPost, "/stations/s1/groups", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1234"
	srv.Handler().ServeHTTP(r, req)
```
（GET 那处 `localReq(http.MethodGet, "/stations/s1")` 保持不变。）

- [ ] **Step 2: 跑测试确认失败**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestStationGroupSettingsSaveAndRender -v`
Expected: FAIL —— 404（路由未注册）/ handler 不存在

- [ ] **Step 3: 写 settings 区段渲染函数 + POST handler**

追加到 `internal/dashboard/dashboard_stations.go` 文件末尾：
```go

// renderGroupSettingsSection renders the inline "分组展示设置" <details> section for
// the station detail page. Lists every group currently polled for the station
// (from LatestGroupRatios) plus any configured-but-currently-absent groups
// (orphans), each with a ☑ visible checkbox + ▲▼ movers + current ratio (RO).
func (s *Server) renderGroupSettingsSection(lang string, stationID string, groupRatios map[string]float64) string {
	cfgs, _ := s.store.GetStationGroupConfigs(context.Background(), stationID)
	byName := map[string]domain.StationGroupConfig{}
	for _, c := range cfgs {
		byName[c.GroupName] = c
	}
	// union: current-poll groups + orphan config groups
	names := map[string]bool{}
	for g := range groupRatios {
		names[g] = true
	}
	for g := range byName {
		names[g] = true
	}
	type row struct {
		name     string
		visible  bool
		order    int
		hasRatio bool
		ratio    float64
	}
	rows := make([]row, 0, len(names))
	for g := range names {
		r := row{name: g}
		if c, ok := byName[g]; ok {
			r.visible, r.order = c.Visible, c.SortOrder
		} else {
			r.visible, r.order = true, 0 // unconfigured → default visible
		}
		if v, ok := groupRatios[g]; ok {
			r.hasRatio, r.ratio = true, v
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].visible != rows[j].visible {
			return rows[i].visible
		}
		if rows[i].order != rows[j].order {
			return rows[i].order < rows[j].order
		}
		return rows[i].name < rows[j].name
	})
	var b strings.Builder
	b.WriteString(`<details class="sec"><summary>` + t(lang, "section.groupsettings") + `</summary>`)
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr><th>` + t(lang, "col.visible") +
		`</th><th>` + t(lang, "col.group") + `</th><th>` + t(lang, "col.groupratio") +
		`</th><th>` + t(lang, "col.order") + `</th></tr></thead><tbody id="tm-gc-body">`)
	for _, r := range rows {
		checked := ""
		if r.visible {
			checked = "checked"
		}
		ratioCell := `<span class="mono p-na">—</span>`
		if r.hasRatio {
			ratioCell = fmt.Sprintf(`<span class="num mono">%.2fx</span>`, r.ratio)
		}
		b.WriteString(fmt.Sprintf(`<tr data-grp="%s"><td><input type="checkbox" name="visible" %s></td>`,
			esc(r.name), checked))
		b.WriteString(`<td><span class="mono">` + esc(r.name) + `</span></td>`)
		b.WriteString(`<td>` + ratioCell + `</td>`)
		b.WriteString(`<td><button class="btn btn-sm btn-outline" onclick="tmGcMove(this,-1)">▲</button> ` +
			`<button class="btn btn-sm btn-outline" onclick="tmGcMove(this,1)">▼</button></td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(`<div class="btn-group" style="margin-top:.8rem"><button class="btn" onclick="tmGcSave('` + esc(stationID) + `')">` +
		t(lang, "btn.savegroupconfig") + `</button> <span id="tm-gc-status" class="meta"></span></div>`)
	b.WriteString(`<script>
function tmGcMove(btn,dir){var tr=btn.closest('tr'),tb=tr.parentNode;
 if(dir<0&&tr.previousElementSibling)tb.insertBefore(tr,tr.previousElementSibling);
 else if(dir>0&&tr.nextElementSibling)tb.insertBefore(tr,tr.nextElementSibling.nextSibling);}
function tmGcSave(id){var rows=document.querySelectorAll('#tm-gc-body tr');
 var gs=[];rows.forEach(function(tr,i){var cb=tr.querySelector('[name=visible]');
 gs.push({group_name:tr.dataset.grp,visible:!!cb.checked,sort_order:i});});
 fetch('/stations/'+id+'/groups',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({groups:gs})})
 .then(function(r){return r.json();}).then(function(d){
  var st=document.getElementById('tm-gc-status');if(st)st.textContent=d.ok?'✓':'✗ '+d.error;
 });}
</script>`)
	b.WriteString(`</details>`)
	return b.String()
}

// groupConfigInput is one row of the POST /stations/{id}/groups payload.
type groupConfigInput struct {
	GroupName string `json:"group_name"`
	Visible   bool   `json:"visible"`
	SortOrder int    `json:"sort_order"`
}

// POST /stations/{id}/groups — replace the station's per-group display config.
// Body: {"groups":[{group_name,visible,sort_order}]}. Returns {"ok":true}.
func (s *Server) stationGroupSettingsSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.findStation(id); !ok {
		writeJSON(w, 404, map[string]string{"error": "station not found"})
		return
	}
	var in struct {
		Groups []groupConfigInput `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json: " + err.Error()})
		return
	}
	cfgs := make([]domain.StationGroupConfig, 0, len(in.Groups))
	for _, g := range in.Groups {
		cfgs = append(cfgs, domain.StationGroupConfig{
			StationID: id, GroupName: g.GroupName, Visible: g.Visible, SortOrder: g.SortOrder,
		})
	}
	if err := s.store.SaveStationGroupConfigs(r.Context(), id, cfgs); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
```

- [ ] **Step 4: 注册路由**

在 `internal/dashboard/dashboard.go` 路由块（105 行 `r.Post("/api/stations/{id}/login", s.stationsLogin)` 之后）加一行：
```go
	r.Post("/stations/{id}/groups", s.stationGroupSettingsSave)
```

- [ ] **Step 5: 在详情页 body 注入区段**

`internal/dashboard/dashboard_stations.go` 的 `stationDetailHTML`，在 body 拼接处（341-352 行的 `body +=` 块），在最后一个 `</details>` 之后、`s.writeHTMLShell(...)` 之前追加一行：
```go
	body += s.renderGroupSettingsSection(lang, id, groupRatios)
```
具体：把 350-351 行那段 `<details class="sec">...probes...</details>` 的结尾 `+` 号之后，紧接添加上面的 `body += ...`。

- [ ] **Step 6: 跑测试确认通过**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestStationGroupSettingsSaveAndRender -v`
Expected: PASS

- [ ] **Step 7: 跑全 dashboard 测试**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/dashboard_stations.go internal/dashboard/dashboard.go internal/dashboard/dashboard_group_config_test.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(dashboard): 详情页内嵌分组展示设置区段 + POST 保存端点"
```

---

### Task 7: 矩阵页 OR-of-visible 行过滤

**Files:**
- Modify: `internal/dashboard/dashboard_pages.go`（`matrixGroups` 383-400；`matrixGroupTable` 417-514）
- Test: `internal/dashboard/dashboard_group_config_test.go`（追加测试）

- [ ] **Step 1: 写失败的测试**

追加到 `internal/dashboard/dashboard_group_config_test.go` 末尾：
```go
func TestMatrixHidesGroupsHiddenEverywhere(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	// both stations carry vip + internal
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{StationID: "s1", ObservedAt: now, GroupRatios: map[string]float64{"vip": 0.5, "internal": 2.0}})
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{StationID: "s2", ObservedAt: now, GroupRatios: map[string]float64{"vip": 0.6, "internal": 2.1}})
	// vip visible on s1 (hidden on s2); internal hidden on both
	saveGroupCfg(t, srv, "s1", []domain.StationGroupConfig{{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0}, {StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0}})
	saveGroupCfg(t, srv, "s2", []domain.StationGroupConfig{{StationID: "s2", GroupName: "vip", Visible: false, SortOrder: 0}, {StationID: "s2", GroupName: "internal", Visible: false, SortOrder: 0}})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "vip") {
		t.Errorf("vip (visible on s1) should be a matrix row:\n%s", body)
	}
	if strings.Contains(body, "internal") {
		t.Errorf("internal (hidden on all stations) should NOT be a matrix row:\n%s", body)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestMatrixHidesGroupsHiddenEverywhere -v`
Expected: FAIL —— body 含 "internal"（尚未按 visible 过滤）

- [ ] **Step 3: 改 `matrixGroupTable` —— 行集合改为 OR-of-visible**

`internal/dashboard/dashboard_pages.go` `matrixGroupTable` 内，把构建 `groupSet` 的那段（423-430 行）：
```go
	rows := make([]stGR, len(sts))
	groupSet := map[string]bool{}
	for i, st := range sts {
		gr, _ := s.store.LatestGroupRatios(context.Background(), st.ID)
		rows[i] = stGR{name: st.Name, gr: gr}
		for g := range gr {
			groupSet[g] = true
		}
	}
```
替换为：
```go
	rows := make([]stGR, len(sts))
	groupSet := map[string]bool{}
	for i, st := range sts {
		gr, _ := s.store.LatestGroupRatios(context.Background(), st.ID)
		rows[i] = stGR{name: st.Name, gr: gr}
		// OR-of-visible: a group appears as a row iff ≥1 station has it visible.
		// Groups with no config row default visible (domain default).
		cfgs, _ := s.store.GetStationGroupConfigs(context.Background(), st.ID)
		hidden := map[string]bool{}
		for _, c := range cfgs {
			if !c.Visible {
				hidden[c.GroupName] = true
			}
		}
		for g := range gr {
			if !hidden[g] {
				groupSet[g] = true
			}
		}
	}
```

- [ ] **Step 4: 改 `matrixGroups` —— 选择器只列可见分组**

`matrixGroups`（383-400 行）替换为：
```go
func (s *Server) matrixGroups(sts []domain.Station, modelFilter string) []string {
	groupSet := map[string]bool{}
	for _, st := range sts {
		obs, _ := s.store.LatestRatioObservations(context.Background(), st.ID)
		cfgs, _ := s.store.GetStationGroupConfigs(context.Background(), st.ID)
		hidden := map[string]bool{}
		for _, c := range cfgs {
			if !c.Visible {
				hidden[c.GroupName] = true
			}
		}
		for _, o := range obs {
			if modelFilter != "" && o.ModelName != modelFilter {
				continue
			}
			if hidden[o.GroupName] {
				continue
			}
			groupSet[o.GroupName] = true
		}
	}
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return groups
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestMatrixHidesGroupsHiddenEverywhere -v`
Expected: PASS

- [ ] **Step 6: 跑既有矩阵测试确认无回归**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestMatrix -v`
Expected: PASS（TestMatrixGroupHTML 不设配置 → 全部默认可见 → 行为不变）

- [ ] **Step 7: Commit**

```bash
git add internal/dashboard/dashboard_pages.go internal/dashboard/dashboard_group_config_test.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(dashboard): 矩阵页按跨站 OR-of-visible 过滤分组行"
```

---

### Task 8: 详情页排序精修 + visible ★ / hidden 灰显标签

**Files:**
- Modify: `internal/dashboard/dashboard_stations.go`（trend sparkline 排序 ~275；ratio table 行排序 ~176-181；分组分隔行 ★/hidden 标签）
- Test: `internal/dashboard/dashboard_group_config_test.go`（追加测试）

- [ ] **Step 1: 写失败的测试（详情页可见分组在前 + hidden 标签）**

追加到 `internal/dashboard/dashboard_group_config_test.go` 末尾：
```go
func TestStationDetailOrdersByConfigAndTagsHidden(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
		StationID: "s1", ObservedAt: now,
		GroupRatios: map[string]float64{"vip": 0.5, "internal": 2.0},
	})
	_ = st.InsertRatioObservations(ctx, []domain.RatioObservation{
		{StationID: "s1", GroupName: "internal", ModelName: "gpt-4o", InputUSDPer1M: 2.5, ObservedAt: now},
		{StationID: "s1", GroupName: "vip", ModelName: "gpt-4o", InputUSDPer1M: 1.0, ObservedAt: now},
	})
	saveGroupCfg(t, srv, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0},
	})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/stations/s1"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	// visible group (vip) must appear before hidden (internal) in the ratio table
	vi := strings.Index(body, `>vip<`)
	ii := strings.Index(body, `>internal<`)
	if vi < 0 || ii < 0 {
		t.Fatalf("both group separators must render:\n%s", body)
	}
	if vi > ii {
		t.Errorf("visible group vip should appear before hidden internal: vi=%d ii=%d", vi, ii)
	}
	if !strings.Contains(body, "已隐藏") {
		t.Errorf("hidden group should carry 已隐藏 tag:\n%s", body)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestStationDetailOrdersByConfigAndTagsHidden -v`
Expected: FAIL —— vip 排在 internal 之后（原按字母序 internal < vip）且无"已隐藏"标签

- [ ] **Step 3: 详情页 ratio table 行按配置排序 + 分组分隔行带 ★/已隐藏**

`internal/dashboard/dashboard_stations.go` `stationDetailHTML` 内，先在加载 `groupRatios` 之后（148 行之后）加载配置，并构造一个 group→display 查找表。把 148 行 `groupRatios, _ := s.store.LatestGroupRatios(ctx, id)` 之后追加：
```go
	stGroupCfgs, _ := s.store.GetStationGroupConfigs(ctx, id)
	groupDisplay := domain.PartitionGroups(groupRatios, stGroupCfgs)
	displayByName := map[string]domain.GroupDisplay{}
	for _, d := range groupDisplay {
		displayByName[d.Name] = d
	}
```
（若 Task 5 Step 6 已加了 `stGroupCfgs, _ := ...`，把它删掉避免重复声明——统一用此处。）

然后把 ratio table 的排序（176-181 行）：
```go
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].o.GroupName != rows[j].o.GroupName {
			return rows[i].o.GroupName < rows[j].o.GroupName
		}
		return rows[i].effIn < rows[j].effIn
	})
```
替换为按配置排序（visible 在前，sort_order 升序，名升序；组内仍按 effIn）：
```go
	sort.SliceStable(rows, func(i, j int) bool {
		gi, gj := displayByName[rows[i].o.GroupName], displayByName[rows[j].o.GroupName]
		// group-level ordering: visible first, then sort_order, then name
		if gi.Visible != gj.Visible {
			return gi.Visible
		}
		if gi.Order != gj.Order {
			return gi.Order < gj.Order
		}
		if rows[i].o.GroupName != rows[j].o.GroupName {
			return rows[i].o.GroupName < rows[j].o.GroupName
		}
		return rows[i].effIn < rows[j].effIn
	})
```

- [ ] **Step 4: 分组分隔行带 ★ / 已隐藏 标签**

`internal/dashboard/ui.go` 的 `renderRatioTable`（468-497 行）当前用 `<span class="grp-tag">` 画分组名。改为接收一个可选的"分组是否可见"判断。**最小改动**：给 `renderRatioTable` 加一个 `visible map[string]bool` 参数（nil = 全可见，向后兼容）。

把 `renderRatioTable` 签名与分隔行改为：
```go
func renderRatioTable(cols []string, rows [][]string, hidden map[string]bool) string {
	var b strings.Builder
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr>`)
	for _, c := range cols {
		b.WriteString("<th>")
		b.WriteString(html.EscapeString(c))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	prev := ""
	for _, row := range rows {
		grp := row[0]
		if grp != prev {
			tag := ""
			if hidden != nil && hidden[grp] {
				tag = ` <span class="badge-sm b-warn">` + "已隐藏" + `</span>`
			} else {
				tag = ` <span class="badge-sm b-cheap">★</span>`
			}
			b.WriteString(`<tr class="grp-sep"><td colspan="` + fmt.Sprint(len(cols)) + `">` +
				`<span class="grp-tag">` + grp + `</span>` + tag + `</td></tr>`)
			prev = grp
		}
		b.WriteString(`<tr>`)
		b.WriteString(`<td class="muted-cell"></td>`)
		for _, cell := range row[1:] {
			b.WriteString("<td>")
			b.WriteString(cell)
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	return b.String()
}
```

更新 `dashboard_stations.go` 347 行的调用点（详情页 ratio table）：
```go
			renderRatioTable([]string{t(lang, "col.group"), t(lang, "col.model"), t(lang, "col.modelratio"), t(lang, "col.completionratio"), t(lang, "col.groupratio"), t(lang, "col.effratio"), t(lang, "col.status")}, ratioRows, hiddenGroupsMap(groupDisplay)) + `</details>` +
```
并在 `dashboard_stations.go` 加一个小助手（紧跟 `renderGroupSettingsSection` 之后）：
```go
// hiddenGroupsMap returns a set of group names that are hidden (not visible)
// for use as renderRatioTable's hidden argument.
func hiddenGroupsMap(displays []domain.GroupDisplay) map[string]bool {
	m := map[string]bool{}
	for _, d := range displays {
		if !d.Visible {
			m[d.Name] = true
		}
	}
	return m
}
```
搜一下是否还有别的 `renderRatioTable(...)` 调用点（`grep -n renderRatioTable internal/dashboard/*.go`）——若有，给它们补 `nil` 第三参（nil = 全可见，向后兼容）。

- [ ] **Step 5: trend sparkline 网格按配置排序**

`dashboard_stations.go` 的 trend 段（260-303 行）当前 `sort.Strings(order)`（275 行）。改为按配置排序：把 275 行 `sort.Strings(order)` 替换为：
```go
		sort.SliceStable(order, func(i, j int) bool {
			gi, gj := displayByName[order[i]], displayByName[order[j]]
			if gi.Visible != gj.Visible {
				return gi.Visible
			}
			if gi.Order != gj.Order {
				return gi.Order < gj.Order
			}
			return order[i] < order[j]
		})
```

- [ ] **Step 6: 跑测试确认通过**

Run: `/home/admin/.local/go/bin/go test ./internal/dashboard/ -run TestStationDetailOrdersByConfigAndTagsHidden -v`
Expected: PASS

- [ ] **Step 7: 跑全量测试 + gofmt**

Run: `/home/admin/.local/go/bin/go test ./... && /home/admin/.local/go/bin/gofmt -w internal/dashboard internal/domain internal/store && /home/admin/.local/go/bin/go build ./...`
Expected: 全部 PASS，gofmt 无 diff，build 成功

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/dashboard_stations.go internal/dashboard/ui.go internal/dashboard/dashboard_group_config_test.go
git commit --author="Devix <devix@transitmonitor.dev>" -m "feat(dashboard): 详情页按配置排序 + visible★/hidden 已隐藏标签"
```

---

### Task 9: 全量验证 + 手动确认

**Files:** 无新文件

- [ ] **Step 1: 全量测试**

Run: `/home/admin/.local/go/bin/go test ./...`
Expected: 全部 PASS

- [ ] **Step 2: gofmt 检查（CI 老红点）**

Run: `/home/admin/.local/go/bin/gofmt -l internal/`
Expected: 无输出（所有文件已格式化）。若有输出，跑 `/home/admin/.local/go/bin/gofmt -w <列出的文件>` 修正后重跑。

- [ ] **Step 3: 跑起来手动验证**

Run（后台启动）:
```bash
pkill transitmonitor 2>/dev/null; nohup ./run.sh >/tmp/tm.log 2>&1 &
```
等待 ~3 秒后浏览器/`curl` 验证：
- `curl -s http://localhost:8080/stations/<某站点id>` 应含"分组展示设置"区段，每组一个 ☑ + ▲▼。
- 取消勾选某分组 → 点"保存分组设置" → 概览页该站点卡上该分组进"另有 N 个已隐藏 ▾"展开器；矩阵页若该组在所有站点都隐藏则该行消失。
- 重新勾选保存 → 恢复。
- 变更 tab 的分组倍率变更行始终展示（不折叠不隐藏）。

- [ ] **Step 4: 最终提交（若有遗留改动）**

```bash
git status --short
# 若有未提交改动：
git add -A && git commit --author="Devix <devix@transitmonitor.dev>" -m "chore: 全量验证收尾"
```

---

## Self-Review

**1. Spec 覆盖核对：**
- §3.1 迁移 → Task 1 ✓
- §3.2 `StationGroupConfig` / §4.1 `GroupDisplay` + `PartitionGroups` → Task 1 ✓（`SplitVisible` 同 Task 1）
- §4.2 新分组默认可见 → `PartitionGroups` 默认 visible=true（Task 1/2 覆盖）✓；隐藏展开器 → Task 5 ✓；孤儿（配置过但缺失）→ `renderGroupSettingsSection` union（Task 6）✓；级联删除 → Task 3 测试 ✓
- §5 Store 4 方法 → Task 3 ✓（`DeleteStationGroupConfig` 加了但 spec 列为 optional，已实现）
- §6 配置入口（详情页内嵌区段 + POST 端点 + AJAX）→ Task 6 ✓
- §7 概览卡 `groupRatioChart` 按 sort_order + 隐藏展开器 → Task 5 ✓
- §8 矩阵 OR-of-visible + `matrixGroups` → Task 7 ✓；★ 角标——**未实现**（spec §8 末行"行内对每个 visible=true 的站点单元格加 ★ 角标"）。这是 spec 明列项，需补。
- §9 详情页全量按配置排序 + ★/已隐藏 + hero/trend 排序 → Task 8 ✓；变更 tab 不动 → 未触碰 `tr.ratio-row`（确认：Task 5-8 均未改 `renderGroupedChangeTable` / `tr.ratio-row`）✓
- §10 i18n 8 个 key → Task 4 ✓
- §11 测试（Store 往返/级联/默认、GroupDisplay 排序、概览/矩阵/详情）→ Task 2/3/5/6/7/8 ✓；gofmt → Task 8/9 ✓

**缺口：spec §8 的矩阵单元格 ★ 角标未实现。** 补一个 Task 7 的子步骤：

在 Task 7 Step 3 改 `matrixGroupTable` 之后，把单元格渲染（503-509 行）改为对可见站点的单元格加 ★。但"该站可见"信息已在 Step 3 的循环里逐站算出 `hidden`——需把它提到 `rows` 结构里。**修订 Task 7**：把 `stGR` 结构改为带 `hidden map[string]bool`，单元格渲染时若 `!hidden[g]` 则在 `gcell` 后加 `★`。

让我把这个修订直接写进 Task 7（见下）。

**2. 占位符扫描：** 无 TBD/TODO/「类似 Task N」；每步都有可执行代码或命令 ✓

**3. 类型一致性：**
- `domain.StationGroupConfig` 字段 `StationID/GroupName/Visible/SortOrder` —— Task 1 定义，Task 3/5/6/7/8 使用一致 ✓
- `domain.GroupDisplay` 字段 `Name/Ratio/Visible/Order` —— Task 1 定义，Task 5/8 使用一致 ✓
- `groupRatioChart([]domain.GroupDisplay, bool)` —— Task 5 改签名，Task 5 Step 6 + Task 8 调用一致 ✓
- `renderRatioTable(cols, rows, hidden map[string]bool)` —— Task 8 改签名，Task 8 Step 4 调用一致；需确认无其他调用点漏改（Task 8 Step 4 已让 grep 处理）✓
- `SaveStationGroupConfigs(ctx, stationID, []StationGroupConfig)` —— Task 3 定义，Task 6 handler 调用一致 ✓

---

### Task 7 修订（补 spec §8 矩阵 ★ 角标）

把 Task 7 Step 3 的 `stGR` 结构与单元格渲染合并修订为：

`matrixGroupTable` 内 `stGR` 与构建循环（418-430 行）替换为：
```go
	type stGR struct {
		name   string
		gr     map[string]float64
		hidden map[string]bool
	}
	rows := make([]stGR, len(sts))
	groupSet := map[string]bool{}
	for i, st := range sts {
		gr, _ := s.store.LatestGroupRatios(context.Background(), st.ID)
		cfgs, _ := s.store.GetStationGroupConfigs(context.Background(), st.ID)
		hidden := map[string]bool{}
		for _, c := range cfgs {
			if !c.Visible {
				hidden[c.GroupName] = true
			}
		}
		rows[i] = stGR{name: st.Name, gr: gr, hidden: hidden}
		for g := range gr {
			if !hidden[g] { // OR-of-visible
				groupSet[g] = true
			}
		}
	}
```
单元格渲染（原 503-509 行）替换为：
```go
		for _, r := range rows {
			v, ok := r.gr[g]
			if !ok {
				row = append(row, `<span class="gcell p-na">—</span>`)
			} else {
				star := ""
				if !r.hidden[g] {
					star = `<span class="gstar">★</span>`
				}
				row = append(row, fmt.Sprintf(`<span class="gcell %s">%.2fx</span>%s`, groupColorClass(v, lo, hi), v, star))
			}
		}
```
并在 `ui.go` CSS（紧挨 `.gcell` 块之后）加：
```css
.gstar{font-size:.7rem;color:var(--primary);margin-left:.15rem}
```
Task 7 Step 5 测试断言补充：body 应含 `★`（vip 在 s1 可见）——在 `TestMatrixHidesGroupsHiddenEverywhere` 末尾加：
```go
	if !strings.Contains(body, "★") {
		t.Errorf("visible station cell should carry ★ marker:\n%s", body)
	}
```

（Task 7 的提交步骤不变。）

---

**计划完成，覆盖 spec 全部条目（含修订补的矩阵 ★ 角标）。**
