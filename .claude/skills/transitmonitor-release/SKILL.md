---
name: transitmonitor-release
description: Cut a TransitMonitor release (tag → CI → verify) and operate the in-panel self-update / rollback. Use when the user asks to publish/tag/version a TransitMonitor release (vX.Y.Z), check release/CI status, fix a broken release, or use the /system 立即更新/回退/重启 buttons. Also triggers on 检查更新/立即更新/回退, "打个 tag", "发版", "推镜像", or GitHub Actions/release failures for TransitMonitor.
---

# TransitMonitor — release & in-panel self-update skill

Covers two flows: (A) cutting a release (tag → CI → verify), (B) the sub2api-style
in-panel 立即更新 / 回退 / 重启 on the `/system` page. Captures every gotcha hit
while building this feature (v0.0.2).

**Repo:** `github.com/yang-yang9/TransitMonitor` (**private**) · push via
`github.alibaba-inc.com` proxy (pushInsteadOf + stored PAT in `~/.git-credentials`).
**Go toolchain:** `/home/admin/.local/go/bin/go` (1.25); system go is 1.23.4 — too old.

---

## A. Cut a release

### 1. Pre-flight (all must be green before tagging)
```bash
cd /home/admin/workspace/code/TransitMonitor
/home/admin/.local/go/bin/gofmt -l .          # MUST be empty — gofmt is the recurring CI red
/home/admin/.local/go/bin/go vet ./...
/home/admin/.local/go/bin/go test -race ./...
/home/admin/.local/go/bin/go build -o /tmp/tm ./cmd/transitmonitor && /tmp/tm -selftest
```
`-selftest` must print `self-test PASSED`. If gofmt lists files → `gofmt -w` them
(even other people's WIP files — it's a mechanical fix and unblocks CI).

### 2. Determine the next version number
**ALWAYS check the remote first** — never assume the local tag list is complete:
```bash
git fetch --tags
git tag -l 'v*' --sort=-version:refname | head -5   # latest tags, newest first
```
Or via the GitHub API (works even when local tags are stale):
```bash
TOK=$(grep 'github.alibaba-inc.com' ~/.git-credentials | grep -oE '://[^:]+:([^@]+)@' | sed 's|://[^:]*:||;s|@||' | head -1)
curl -s -H "Authorization: Bearer $TOK" https://api.github.com/repos/yang-yang9/TransitMonitor/releases/latest \
  | python3 -c "import sys,json;print('latest:',json.load(sys.stdin).get('tag_name','(none)'))"
```
Increment the patch (or minor/major) from the **highest existing tag**. Do NOT
reuse an existing version number — force-pushing a tag overwrites the prior
release's assets and confuses anyone who already pulled that version.

### 3. Tag + push
```bash
git tag -a vX.Y.Z -m "vX.Y.Z: <one-line summary>"
git push origin vX.Y.Z
```
Push goes through the Alibaba proxy automatically (pushInsteadOf is configured).

### 4. Two workflows trigger on `v*` tags
- **`release.yml`** — builds `transitmonitor_{linux,darwin}_{amd64,arm64}.tar.gz` +
  `checksums.txt` (sha256sum format), uploads to a GitHub Release (auto notes).
- **`docker.yml`** — builds `linux/amd64,linux/arm64` image →
  `ghcr.io/yang-yang9/transitmonitor:vX.Y.Z` + `:latest`, injects
  `VERSION=<tag>` via build-args (`-ldflags "-X main.version=<tag>"`).

### 5. Monitor CI (repo is private → API needs the PAT)
`gh` CLI isn't installed; use the API with the PAT already in `~/.git-credentials`
(the `github.alibaba-inc.com` PAT works against `api.github.com`):
```bash
TOK=$(grep 'github.alibaba-inc.com' ~/.git-credentials | grep -oE '://[^:]+:([^@]+)@' | sed 's|://[^:]*:||;s|@||' | head -1)
# run status
curl -s -H "Authorization: Bearer $TOK" "https://api.github.com/repos/yang-yang9/TransitMonitor/actions/runs?per_page=8" \
  | python3 -c "import sys,json;[print(r['created_at'],r['name'],r['status'],r['conclusion'],r['head_branch']) for r in json.load(sys.stdin)['workflow_runs']]"
# release assets
curl -s -H "Authorization: Bearer $TOK" https://api.github.com/repos/yang-yang9/TransitMonitor/releases/latest \
  | python3 -c "import sys,json;d=json.load(sys.stdin);print('tag:',d.get('tag_name'),'assets:',[a['name'] for a in d.get('assets',[])])"
```
Docker multi-arch (arm64 via QEMU) takes ~5-8 min. Poll with `sleep 180` between checks.

### 6. If a workflow fails — fetch the failing step + logs
```bash
RUN_ID=<from-the-runs-list>
curl -s -H "Authorization: Bearer $TOK" "https://api.github.com/repos/yang-yang9/TransitMonitor/actions/runs/$RUN_ID/jobs" \
  | python3 -c "import sys,json;[print('FAIL step:',s['name']) for j in json.load(sys.stdin)['jobs'] for s in j['steps'] if s['conclusion']=='failure']"
curl -sL -H "Authorization: Bearer $TOK" -o /tmp/logs.zip "https://api.github.com/repos/yang-yang9/TransitMonitor/actions/runs/$RUN_ID/logs"
python3 -c "import zipfile;z=zipfile.ZipFile('/tmp/logs.zip');[print(n,'|',l[:200]) for n in z.namelist() for l in z.read(n).decode('utf-8','replace').splitlines() if any(k in l.lower() for k in ['error','fail','denied','not found','panic'])]"
```

### 7. Common CI failures → fixes
- **docker.yml: `COPY scripts/entrypoint.sh: not found` / `failed to calculate checksum`** →
  `.dockerignore` excludes `scripts/`. Add `!scripts/entrypoint.sh` under the
  `scripts/` line. (Hit on v0.0.2.)
- **ci.yml: `needs gofmt`** → `gofmt -l .` locally, `gofmt -w` the listed files.
- **docker.yml: VERSION still `dev`** → confirm `docker.yml` has
  `build-args: VERSION=${{ github.ref_name }}` and Dockerfile has `ARG VERSION` +
  `-X main.version=${VERSION}`.

### 8. Re-tagging a broken release (image build failed)
The cleanest fix when a tag's image didn't publish: fix on `main`, push, move the
tag to the fixed commit, force-push (re-runs both workflows; `release.yml` updates
the existing Release, binaries are rebuilt identical):
```bash
git tag -d vX.Y.Z
git tag -a vX.Y.Z -m "..." <fixed-commit-sha>
git push origin vX.Y.Z --force
```
⚠ Force-push tag is destructive — confirm with the user first (see AskUserQuestion).

---

## B. In-panel self-update / rollback (the `/system` page)

### Runtime prerequisites (all three required, else it silently 404s / breaks)
1. **Start via `./run.sh`**, NOT bare `./transitmonitor`. `run.sh` sources `.env`
   which has `TRANSMONITOR_ENCRYPTION_KEY` — without it DB stations don't decrypt
   and load as 0. (Memory: `run-sh-env-vars`.)
2. **Private repo → set `TRANSMONITOR_UPDATE_GITHUB_TOKEN`** in `.env` (or compose
   `environment`). Needs `repo` scope. The updater polls
   `api.github.com/repos/yang-yang9/TransitMonitor/releases/latest` — unauthenticated
   calls to a private repo return **404** (this is the #1 cause of "检查更新 报 404",
   NOT a missing release). Token is sent only to `api.github.com`, stripped on
   redirect off that host.
3. **Docker: pull a wrapper-enabled image (v0.0.2+) at least once.** The wrapper
   (`scripts/entrypoint.sh`) execs `/data/bin/transitmonitor` if an in-panel update
   placed one there (persists across container recreation), else `/app/transitmonitor`.
   `TRANSMONITOR_WRAPPER=1` marks wrapper-in-effect → `WrapperReady()` true →
   `/system` enables 立即更新. Without the wrapper, an in-panel upgrade would be
   lost on `compose up -d --build`; the page detects this and disables Upgrade
   with a "re-pull v0.0.2+ image" hint.

### Operating the page (browser at `/system`)
- **检查更新** → `GET /api/system/check-updates?force=true`. Status pill:
  idle/ok(newer? no)/new(有新版本)/err. Soft-fails to `error` field if GitHub
  unreachable (200, not 500) so the page degrades.
- **立即更新** → `POST /api/system/upgrade` → download platform tar.gz + checksums.txt
  → SHA256 verify → extract `transitmonitor` (Zip-Slip guarded) → atomic rename over
  the active binary (archiving the old one by version). Returns `need_restart:true`.
- **回退** → `POST /api/system/rollback` body `{"version":"x.y.z"}` (empty = most
  recent local backup). Candidates from `/api/system/rollback-versions` —
  **local manifest only** (TransitMonitor doesn't publish historical binaries;
  sub2api's online 15→3 list isn't usable here). Retains 3.
- **重启** → `POST /api/system/restart` → bare: `syscall.Exec` (PID preserved;
  preRestart hook flushes SQLite WAL + stops HTTP server first). docker:
  `os.Exit(0)` + `restart: unless-stopped` + wrapper re-execs `/data/bin`.

### Verify via curl (auth: Bearer token OR password login OR PUBLIC=1)
```bash
# password mode → login first
curl -s -c /tmp/c.txt -X POST -H 'Content-Type: application/json' -d '{"password":"<pwd>"}' http://127.0.0.1:7421/api/login
curl -s -b /tmp/c.txt http://127.0.0.1:7421/api/system/version          # {current, mode, wrapper_ready}
curl -s -b /tmp/c.txt "http://127.0.0.1:7421/api/system/check-updates?force=1"  # {current_version, latest_version, has_update, error?}
```

### Version-string gotcha
`make build` produces `0.1.0-dev`. `compareVersions` treats any `*-dev` as `0.0.0`,
so a dev build always reports "update available" to any real tag — correct
behavior. Self-update is intended for Release builds (the released image reports
the real tag via `-X main.version`).

---

## C. Restarting the local server (gotchas)
- **`pkill -f transitmonitor` self-kills the Bash tool shell** (exit 144) because
  `-f` matches the shell's own command line (it contains "transitmonitor"). Use
  `pkill transitmonitor` (no `-f`; matches the binary name only). (Memory: `pkill-f-self-kill`.)
- Start: `nohup ./run.sh > /tmp/tm.log 2>&1 &` — survives across turns. `run_in_background: true`
  Bash tasks get reaped at turn-end by the harness; prefer plain `nohup &`.
- Port-forward for browser access: `curl -s "http://localhost:58596/api/port-mapping?port=7421"` → public HTTPS URL. Bind `TRANSMONITOR_DASHBOARD_ADDR=0.0.0.0:7421` or external proxy 502s.

---

## Reference
- General ops (build/run/config/dashboard/troubleshoot): the `transitmonitor` skill.
- Full manual: `docs/usage.md` §9b (in-panel update) + §11 (security).
- Implementation: `internal/updater/updater.go`, `internal/dashboard/dashboard_system.go`.
- Memory: `in-panel-self-update`, `transitmonitor-github-repo`, `run-sh-env-vars`,
  `pkill-f-self-kill`, `transitmonitor-go-toolchain`, `commit-as-devix-no-claude-trailer`.
