#!/usr/bin/env bash
# tm-session.sh — 为一个 TransitMonitor 任务建立独立 worktree，避免多会话并发冲突。
#
# 用法：
#   tm-session.sh <task-name> [base-ref]
#
# 行为：
#   - 在主仓库 /home/admin/workspace/code/TransitMonitor 下创建 worktree ../tm-<task>
#   - 新建分支 feat/<task>（默认基于当前 HEAD；可指定 base-ref）
#   - 切进 worktree；交互式终端会 exec 一个新 shell
#
# 完成后回主仓库合并：
#   cd /home/admin/workspace/code/TransitMonitor
#   git merge --no-ff feat/<task>
#   git worktree remove ../tm-<task>
set -euo pipefail

REPO="/home/admin/workspace/code/TransitMonitor"
cd "$REPO"

TASK="${1:?用法: tm-session.sh <task-name> [base-ref]}"
BASE="${2:-}"
BRANCH="feat/${TASK}"
WT="../tm-${TASK}"

if [ -d "$WT" ]; then
  echo "ℹ️  worktree $WT 已存在，直接进入" >&2
else
  if [ -n "$BASE" ]; then
    git worktree add "$WT" -b "$BRANCH" "$BASE"
  else
    git worktree add "$WT" -b "$BRANCH"
  fi
fi

cd "$WT"
echo "✅ worktree: $WT"
echo "   branch:   $BRANCH"
echo "   go:       /home/admin/.local/go/bin/go"
echo "   合并回 trunk:  cd $REPO && git merge --no-ff $BRANCH && git worktree remove $WT"

if [ -t 0 ]; then
  exec bash
fi
