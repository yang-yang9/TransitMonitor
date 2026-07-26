#!/usr/bin/env bash
# PreToolUse hook: 禁止在主 checkout 直接编辑文件，强制走 git worktree，
# 防止多个并发 Claude/human 会话共享同一工作区导致的 git 冲突。
#
# 拦截 Edit / Write / MultiEdit / NotebookEdit 中指向主 checkout 的 file_path
# （或 notebook_path）。worktree 路径不受限。
#
# 临时绕过（仅限有意为之的 trunk 编辑，如修本规则）：
#   TM_ALLOW_TRUNK=1
set -u

# 逃生舱
[ "${TM_ALLOW_TRUNK:-}" = "1" ] && exit 0

# 读 PreToolUse 的工具输入（JSON on stdin）
input="$(cat)"

# 抽取 file_path 或 notebook_path（取第一个匹配）
fp="$(printf '%s' "$input" | sed -nE 's/.*"(file_path|notebook_path)"[[:space:]]*:[[:space:]]*"([^"]*)".*/\2/p' | head -n1)"
[ -z "$fp" ] && exit 0   # 无文件路径 → 放行

# 解析为绝对路径
case "$fp" in
  /*) abspath="$fp" ;;
  *)  abspath="$PWD/$fp" ;;
esac

MAIN="/home/admin/workspace/code/TransitMonitor"
case "$abspath" in
  "$MAIN"/.claude/worktrees/*) exit 0 ;;   # worktree（EnterWorktree 位置）→ 放行
  "$MAIN"/*) ;;                              # 主 checkout → 拦
  *) exit 0 ;;                                # 仓库外 → 放行
esac

# 落在主 checkout → 阻断（exit 2 + stderr 展示给调用方）
cat >&2 <<'EOF'
⛔ 主 checkout 禁止直接改代码（防止多会话并发 git 冲突）。
   请在 worktree 里改：
     tm-session.sh <task-name>
     # 或: git worktree add ../tm-<task> -b feat/<task> && cd ../tm-<task>
   详见 CLAUDE.md。
   临时绕过（仅限有意为之的 trunk 编辑）：TM_ALLOW_TRUNK=1
EOF
exit 2
