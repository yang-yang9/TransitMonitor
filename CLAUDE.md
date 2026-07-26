# TransitMonitor 开发规则（全员必读）

本机有多个 Claude auto-mode 会话并发运行。**禁止在主 checkout
(`/home/admin/workspace/code/TransitMonitor`) 直接改代码** —— 并发会话共享同一工作区 /
index 会导致：

- `git add -A` 把别人的改动扫进自己的提交（commit message 跟内容对不上）
- `git commit --amend` / `git reset` 抹掉别人刚提交的改动
- `git reset --hard` 毁掉别人未提交的工作区

历史教训：2026-07-26 提交告警规则功能时，8 个文件被并发 agent 搅得分两次才全进 HEAD，
中间还被 amend 丢过一次。根因就是共享 checkout。

## 统一开发流程

**每次开发（无论人或 agent）必须在独立 worktree 里进行：**

1. 开工先建 worktree（独立目录 + 独立分支）：
   ```bash
   tm-session.sh <task-name>          # 自动建 ../tm-<task> 并切到 feat/<task>
   # 或手动：
   git worktree add ../tm-<task> -b feat/<task> && cd ../tm-<task>
   ```
2. 所有编辑、`go build`、`go test`、`git add`、`git commit` 都在该 worktree 内完成。
3. `git add` 只圈定具体文件，**禁止 `git add -A`**（避免扫进无关改动）。
4. 提交作者 `Devix <devix@transitmonitor.dev>`，不带 `Co-Authored-By: Claude` trailer。
5. 构建用 `/home/admin/.local/go/bin/go`（1.25）；提交前必过
   `make vet && make fmt-check && make selftest`（gofmt 是反复 CI 红点）。
6. 完成后回主仓库合并，再清 worktree：
   ```bash
   cd /home/admin/workspace/code/TransitMonitor
   git merge --no-ff feat/<task>
   git worktree remove ../tm-<task>
   ```
   合并冲突在 merge 阶段正常解决 —— 这是干净的、可审阅的冲突，不是静默的半路覆盖。

## 硬约束（PreToolUse hook）

`.claude/settings.json` 注册了一个 PreToolUse hook，拦截在主 checkout 里的
`Edit` / `Write` / `MultiEdit` / `NotebookEdit`，强制走 worktree。

- worktree 路径（`../tm-*` 或 `.claude/worktrees/*`）内编辑**不受限**。
- 临时绕过（仅限有意为之的 trunk 编辑，如修本规则本身）：
  ```bash
  TM_ALLOW_TRUNK=1
  ```
- 想完全卸载约束：删 `.claude/settings.json` 里的 hooks 条目。

## 主 checkout 只用于

`git pull` / `git merge` / `git push` / `git worktree` / 跑 selftest 验证 trunk ——
**不做代码编辑**。
