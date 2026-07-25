// Package updater implements an in-panel "立即更新 / 回退" (update / rollback)
// feature for TransitMonitor, modeled on sub2api's update_service.go.
//
// Flow:
//   - CheckUpdates polls api.github.com/repos/{repo}/releases/latest (20min cache,
//     force bypass) and compares the latest tag to the running version.
//   - PerformUpdate downloads the platform archive + checksums.txt from the
//     release, verifies the SHA256, extracts the `transitmonitor` binary
//     (Zip-Slip guarded), and atomically renames it over the active binary
//     (keeping the previous one as a backup).
//   - Rollback restores the most recent local backup; RollbackToVersion
//     restores a specific locally-archived version.
//   - Restart swaps in the new binary: bare-binary mode uses syscall.Exec
//     (in-place image replace, PID preserved); Docker mode os.Exit(0)s and
//     relies on the container supervisor + wrapper entrypoint to re-exec the
//     binary stored under /data/bin (which survives container recreation).
//
// TransitMonitor-specific constraint: in the canonical Docker deployment the
// binary at /app/transitmonitor lives in the image layer and is lost on
// container recreation, and only /data is persisted. So in Docker mode the
// updater stages the new binary under /data (same volume → atomic rename onto
// /data/bin/transitmonitor) and the wrapper entrypoint prefers that path.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Defaults — mirror sub2api's update_service.go constants.
const (
	defaultRepo               = "yang-yang9/TransitMonitor"
	maxDownloadSize     int64 = 500 * 1024 * 1024 // 500 MiB cap on archive + binary
	maxRollbackVersions       = 3                 // local backups retained
	updateCacheTTL            = 20 * time.Minute
	binaryName                = "transitmonitor"
	downloadTimeout           = 10 * time.Minute
	apiTimeout                = 30 * time.Second
)

// Mode selects the swap/restart strategy.
type Mode string

const (
	ModeBare   Mode = "bare"   // ./transitmonitor -config ... — syscall.Exec restart
	ModeDocker Mode = "docker" // container — os.Exit(0) + supervisor + wrapper
)

// UpdateInfo is the result of CheckUpdates (also rendered on the /system page).
type UpdateInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	ReleaseURL     string `json:"release_url,omitempty"`
	ReleaseNotes   string `json:"release_notes,omitempty"`
	PublishedAt    string `json:"published_at,omitempty"`
	CheckedAt      int64  `json:"checked_at"`
	// Error is set when GitHub was unreachable so the page can show a hint
	// instead of silently reporting "up to date".
	Error string `json:"error,omitempty"`
}

// UpdateOutcome is returned by PerformUpdate / Rollback*.
type UpdateOutcome struct {
	Message     string `json:"message"`
	NeedRestart bool   `json:"need_restart"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
}

// RollbackVersion is one locally-archived prior binary the user can revert to.
type RollbackVersion struct {
	Version     string `json:"version"`
	PublishedAt int64  `json:"published_at"`
	SHA256      string `json:"sha256"`
}

// Service performs update / rollback / restart operations.
type Service struct {
	version string // running version (from -ldflags -X main.version)
	repo    string // "owner/name" on github.com
	token   string // optional GitHub Bearer token (raises API rate limits)

	mode    Mode
	exePath string // resolved running binary path (bare mode swap target)
	dataDir string // persisted writable dir (/data in docker, exeDir in bare)

	preRestart func() error // hook: flush+close DB (and dashboard) before exec/exit

	mu      sync.Mutex
	busy    bool       // serializes upgrade/rollback (restart has its own guard)
	cache   *ghRelease // 20-min cache of the latest release
	cacheAt time.Time

	restarting atomic.Int32 // 0→1 once a restart is scheduled (prevents double-restart)
}

// SetPreRestart wires the pre-restart hook (close DB to flush SQLite WAL before
// syscall.Exec; graceful dashboard shutdown). Called by main after construction.
func (s *Service) SetPreRestart(f func() error) { s.preRestart = f }

// CurrentVersion returns the running version string.
func (s *Service) CurrentVersion() string { return s.version }

// Mode returns "bare" or "docker".
func (s *Service) Mode() string { return string(s.mode) }

// WrapperReady reports whether the Docker wrapper entrypoint is in effect —
// i.e. the running binary is the one under /data/bin (the persisted, swappable
// path). If false in docker mode, an in-panel upgrade would not survive a
// container recreation, so the UI must tell the operator to re-pull a
// wrapper-enabled image first.
func (s *Service) WrapperReady() bool {
	if s.mode != ModeDocker {
		return true
	}
	// Running from the persisted /data/bin path → wrapper routed here.
	if strings.HasPrefix(s.exePath, filepath.Join(s.dataDir, "bin")) {
		return true
	}
	// Heuristic: the marker env the wrapper/image sets.
	if os.Getenv("TRANSMONITOR_WRAPPER") == "1" {
		return true
	}
	// /data/bin/<binary> already exists → wrapper would route to it.
	_, err := os.Stat(filepath.Join(s.dataDir, "bin", binaryName))
	return err == nil
}

// New constructs a Service. dataDir is the persisted writable directory
// (/data in docker, the binary's dir in bare mode). repo may be "" for default.
func New(version, dataDir, repo, token string) (*Service, error) {
	if repo == "" {
		repo = defaultRepo
	}
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("os.Executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	mode := detectMode(exePath, dataDir)
	if dataDir == "" {
		if mode == ModeDocker {
			dataDir = "/data"
		} else {
			dataDir = filepath.Dir(exePath)
		}
	}
	return &Service{
		version: version,
		repo:    repo,
		token:   token,
		mode:    mode,
		exePath: exePath,
		dataDir: dataDir,
	}, nil
}

func detectMode(exePath, dataDir string) Mode {
	switch os.Getenv("TRANSMONITOR_RUN_MODE") {
	case "docker", "container":
		return ModeDocker
	case "bare":
		return ModeBare
	}
	// Heuristics: explicit container marker env, or running inside the image.
	if os.Getenv("TRANSMONITOR_CONTAINER") == "1" {
		return ModeDocker
	}
	if strings.HasPrefix(exePath, "/app/") || exePath == "/app/transitmonitor" {
		return ModeDocker
	}
	if strings.HasPrefix(exePath, "/data/bin/") {
		return ModeDocker
	}
	if dataDir == "/data" {
		return ModeDocker
	}
	return ModeBare
}

// ---- path layout -----------------------------------------------------------

// activePath is the binary the supervisor/wrapper will exec next restart.
func (s *Service) activePath() string {
	if s.mode == ModeDocker {
		return filepath.Join(s.dataDir, "bin", binaryName)
	}
	return s.exePath
}

// backupDir holds the retained prior binaries + manifest.
func (s *Service) backupDir() string {
	if s.mode == ModeDocker {
		return filepath.Join(s.dataDir, ".updates", "backup")
	}
	return filepath.Join(filepath.Dir(s.exePath), ".transitmonitor-backups")
}

// manifestPath is where the backup list lives.
func (s *Service) manifestPath() string { return filepath.Join(s.backupDir(), "manifest.json") }

// stagingDir is a temp dir on the SAME filesystem as the swap target, so that
// os.Rename onto the active binary is atomic.
func (s *Service) stagingDir() (string, error) {
	base := filepath.Dir(s.activePath())
	if s.mode == ModeBare {
		base = filepath.Dir(s.exePath)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, ".transitmonitor-update-*")
}

// ---- GitHub releases -------------------------------------------------------

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt string    `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// apiClient builds an http.Client for api.github.com calls. The Bearer token is
// only attached to api.github.com (stripped on redirect off that host), matching
// sub2api's github_release_service.go behavior.
func (s *Service) apiClient() *http.Client {
	return &http.Client{Timeout: apiTimeout}
}

func (s *Service) apiURL(path string) string {
	return "https://api.github.com/repos/" + s.repo + path
}

// doAPI sends a GET to api.github.com with an optional Bearer token attached
// only to the API host. Follows redirects manually so the token never leaks to
// a redirected-off host (e.g. objects.githubusercontent.com download URLs).
func (s *Service) doAPI(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "TransitMonitor-updater")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	client := s.apiClient()
	// Do not follow redirects automatically: the token must not be sent to a
	// redirected host. The API itself returns JSON, not a redirect.
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("github api %s: %s", resp.Status, truncate(string(body), 200))
	}
	return json.Unmarshal(body, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// fetchLatestRelease returns the latest non-draft release (cached 20min unless force).
func (s *Service) fetchLatestRelease(ctx context.Context, force bool) (*ghRelease, error) {
	s.mu.Lock()
	if !force && s.cache != nil && time.Since(s.cacheAt) < updateCacheTTL {
		c := s.cache
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	var rel ghRelease
	if err := s.doAPI(ctx, s.apiURL("/releases/latest"), &rel); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache = &rel
	s.cacheAt = time.Now()
	s.mu.Unlock()
	return &rel, nil
}

// CheckUpdates compares the latest release tag to the running version.
func (s *Service) CheckUpdates(ctx context.Context, force bool) (UpdateInfo, error) {
	info := UpdateInfo{CurrentVersion: s.version, CheckedAt: time.Now().Unix()}
	rel, err := s.fetchLatestRelease(ctx, force)
	if err != nil {
		info.Error = err.Error()
		return info, nil // soft-fail: surface the error to the UI, not a 500
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	info.LatestVersion = latest
	info.ReleaseURL = rel.HTMLURL
	info.ReleaseNotes = rel.Body
	info.PublishedAt = rel.PublishedAt
	info.HasUpdate = compareVersions(s.version, latest) < 0
	return info, nil
}

// ---- download / verify / extract -------------------------------------------

// validateDownloadURL enforces HTTPS + a GitHub-owned host (SSRF guard).
func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("bad url: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("download must be https, got %q", u.Scheme)
	}
	host := u.Hostname()
	switch {
	case host == "github.com", host == "objects.githubusercontent.com", host == "codeload.github.com":
		return nil
	case strings.HasSuffix(host, ".githubusercontent.com"):
		return nil
	default:
		return fmt.Errorf("download host %q not allowed", host)
	}
}

// downloadFile fetches url into dst with a size cap (io.LimitReader) and a long
// timeout. The Go default transport honors HTTP_PROXY/HTTPS_PROXY from env,
// which matters inside the Alibaba inner network.
func (s *Service) downloadFile(ctx context.Context, url, dst string) error {
	if err := validateDownloadURL(url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TransitMonitor-updater")
	// NOTE: no Authorization header here — download URLs redirect off the API
	// host to objects.githubusercontent.com; a Bearer token would leak.
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, maxDownloadSize))
	return err
}

// verifyChecksum matches the archive's sha256 against a "hash  filename" line
// in checksums.txt (sub2api format).
func verifyChecksum(checksums, archiveName, archivePath string) error {
	got, err := fileSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	want := ""
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		h, name := fields[0], fields[1]
		if name == archiveName || filepath.Base(name) == filepath.Base(archiveName) {
			want = h
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum entry for %q in checksums.txt", archiveName)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxDownloadSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary pulls the entry named binaryName (or binaryName.exe) out of a
// gzip+tar archive into outPath. Zip-Slip guarded; size-capped.
func extractBinary(archivePath, outPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(io.LimitReader(f, maxDownloadSize))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, maxDownloadSize))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Zip-Slip guard: reject any path that escapes the extraction base.
		if strings.Contains(filepath.Clean(hdr.Name), "..") {
			return fmt.Errorf("refused tar entry with .. : %q", hdr.Name)
		}
		base := filepath.Base(hdr.Name)
		if base != binaryName && base != binaryName+".exe" {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive entry %q is not a regular file", hdr.Name)
		}
		out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxDownloadSize)); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("binary %q not found in archive", binaryName)
}

// ---- manifest ---------------------------------------------------------------

type backupEntry struct {
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Path       string `json:"path"`
	ArchivedAt int64  `json:"archived_at"`
}

type manifest struct {
	Backups []backupEntry `json:"backups"`
}

func (s *Service) loadManifest() manifest {
	var m manifest
	b, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

func (s *Service) saveManifest(m manifest) error {
	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.manifestPath(), b, 0o644)
}

// archiveBackup records the just-replaced binary as a rollback candidate,
// retaining only the most recent maxRollbackVersions. destName is the version
// the replaced binary was running (used as the on-disk filename + manifest key).
func (s *Service) archiveBackup(oldBinaryPath, oldVersion string) error {
	if oldVersion == "" {
		oldVersion = "unknown"
	}
	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		return err
	}
	dst := filepath.Join(s.backupDir(), oldVersion+".bin")
	// copy (not rename) so the file at oldBinaryPath is unaffected if the
	// caller still needs it — and so we keep a stable per-version file.
	if err := copyFile(oldBinaryPath, dst); err != nil {
		return err
	}
	sum, _ := fileSHA256(dst)
	m := s.loadManifest()
	// drop any prior entry for the same version, then prepend.
	out := make([]backupEntry, 0, len(m.Backups)+1)
	out = append(out, backupEntry{Version: oldVersion, SHA256: sum, Path: dst, ArchivedAt: time.Now().Unix()})
	for _, e := range m.Backups {
		if e.Version == oldVersion {
			continue
		}
		out = append(out, e)
	}
	// retain newest maxRollbackVersions; remove the rest from disk.
	if len(out) > maxRollbackVersions {
		for _, e := range out[maxRollbackVersions:] {
			_ = os.Remove(e.Path)
		}
		out = out[:maxRollbackVersions]
	}
	return s.saveManifest(manifest{Backups: out})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, io.LimitReader(in, maxDownloadSize))
	return err
}

// ---- the swap --------------------------------------------------------------

// applyRelease downloads + verifies + extracts + atomically swaps the binary
// from rel into the active path, archiving the previous binary first.
func (s *Service) applyRelease(ctx context.Context, rel *ghRelease) error {
	archiveAsset, sumAsset := pickAssets(rel.Assets)
	if archiveAsset == nil {
		return errors.New("no platform archive found in release assets")
	}
	if sumAsset == nil {
		return errors.New("no checksums.txt found in release assets")
	}

	staging, err := s.stagingDir()
	if err != nil {
		return fmt.Errorf("staging dir: %w", err)
	}
	defer os.RemoveAll(staging)
	archivePath := filepath.Join(staging, filepath.Base(archiveAsset.Name))
	if err := s.downloadFile(ctx, archiveAsset.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	// checksums.txt is small; download into memory.
	sumTmp := filepath.Join(staging, "checksums.txt")
	if err := s.downloadFile(ctx, sumAsset.BrowserDownloadURL, sumTmp); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	sumBytes, err := os.ReadFile(sumTmp)
	if err != nil {
		return err
	}
	if err := verifyChecksum(string(sumBytes), filepath.Base(archiveAsset.Name), archivePath); err != nil {
		return err
	}

	newBin := filepath.Join(staging, binaryName)
	if err := extractBinary(archivePath, newBin); err != nil {
		return err
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		return err
	}

	// Archive the binary we are about to replace, keyed by the version it ran.
	oldVersion := s.version
	active := s.activePath()
	// In docker mode the active path may not exist yet (first upgrade from the
	// image-baked binary). Archive the running exePath instead if active is
	// missing — that is what's actually running.
	runningExe := s.exePath
	if _, err := os.Stat(active); err == nil {
		runningExe = active
	}
	if _, err := os.Stat(runningExe); err == nil {
		if err := s.archiveBackup(runningExe, oldVersion); err != nil {
			// non-fatal: we still swap, just can't roll back to old.
			fmt.Fprintf(os.Stderr, "updater: archive backup (non-fatal): %v\n", err)
		}
	}

	// Ensure the target dir exists (docker: /data/bin may not yet).
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		return err
	}
	// Atomic swap. If the final rename fails, leave the previous binary in
	// place (do NOT clobber it) so the service still runs.
	tmp := active + ".new"
	if err := os.Rename(newBin, tmp); err != nil {
		return fmt.Errorf("stage rename: %w", err)
	}
	if err := os.Rename(tmp, active); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("swap rename: %w", err)
	}
	return nil
}

// pickAssets selects the platform archive (name contains GOOS_GOARCH) and the
// checksums.txt asset from a release's asset list.
func pickAssets(assets []ghAsset) (archive, checksums *ghAsset) {
	want := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	for i := range assets {
		a := &assets[i]
		if strings.Contains(a.Name, want) && (strings.HasSuffix(a.Name, ".tar.gz") || strings.HasSuffix(a.Name, ".tar") || strings.HasSuffix(a.Name, ".zip")) {
			if archive == nil {
				archive = a
			}
		}
		if a.Name == "checksums.txt" || a.Name == "checksum.txt" || strings.Contains(a.Name, "checksums") {
			if checksums == nil {
				checksums = a
			}
		}
	}
	return archive, checksums
}

// ---- public operations ------------------------------------------------------

var errBusy = errors.New("another update/rollback operation is in progress")

func (s *Service) lock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

func (s *Service) unlock() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// PerformUpdate downloads + swaps the latest release binary.
func (s *Service) PerformUpdate(ctx context.Context) (UpdateOutcome, error) {
	if !s.lock() {
		return UpdateOutcome{}, errBusy
	}
	defer s.unlock()
	rel, err := s.fetchLatestRelease(ctx, true)
	if err != nil {
		return UpdateOutcome{}, fmt.Errorf("check latest: %w", err)
	}
	from := s.version
	if err := s.applyRelease(ctx, rel); err != nil {
		return UpdateOutcome{}, err
	}
	to := strings.TrimPrefix(rel.TagName, "v")
	s.version = to // in-memory; the new binary reports its own -ldflags version after restart
	return UpdateOutcome{
		Message:     "Update completed. Restart TransitMonitor to run the new version.",
		NeedRestart: true,
		FromVersion: from,
		ToVersion:   to,
	}, nil
}

// ListRollbackVersions returns the locally-archived prior binaries (newest first).
func (s *Service) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	m := s.loadManifest()
	out := make([]RollbackVersion, 0, len(m.Backups))
	for _, e := range m.Backups {
		out = append(out, RollbackVersion{Version: e.Version, PublishedAt: e.ArchivedAt, SHA256: e.SHA256})
	}
	return out, nil
}

// Rollback restores the most recent local backup (the version running before
// the last upgrade).
func (s *Service) Rollback(ctx context.Context) (UpdateOutcome, error) {
	if !s.lock() {
		return UpdateOutcome{}, errBusy
	}
	defer s.unlock()
	m := s.loadManifest()
	if len(m.Backups) == 0 {
		return UpdateOutcome{}, errors.New("no backup available — nothing to roll back to")
	}
	target := m.Backups[0]
	return s.swapFromBackup(target, s.version)
}

// RollbackToVersion restores a specific locally-archived version. The version
// must exist in the manifest or the request is rejected.
func (s *Service) RollbackToVersion(ctx context.Context, version string) (UpdateOutcome, error) {
	if !s.lock() {
		return UpdateOutcome{}, errBusy
	}
	defer s.unlock()
	m := s.loadManifest()
	for _, e := range m.Backups {
		if e.Version == version {
			return s.swapFromBackup(e, s.version)
		}
	}
	return UpdateOutcome{}, fmt.Errorf("version %q is not in the rollback list", version)
}

func (s *Service) swapFromBackup(entry backupEntry, fromVersion string) (UpdateOutcome, error) {
	if _, err := os.Stat(entry.Path); err != nil {
		return UpdateOutcome{}, fmt.Errorf("backup binary missing on disk: %w", err)
	}
	active := s.activePath()
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		return UpdateOutcome{}, err
	}
	// Archive the currently-running binary first (so we can roll back to it
	// later), keyed by its version.
	runningExe := s.exePath
	if _, err := os.Stat(active); err == nil {
		runningExe = active
	}
	if _, err := os.Stat(runningExe); err == nil {
		_ = s.archiveBackup(runningExe, fromVersion)
	}
	tmp := active + ".rollback"
	if err := copyFile(entry.Path, tmp); err != nil {
		return UpdateOutcome{}, err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return UpdateOutcome{}, err
	}
	if err := os.Rename(tmp, active); err != nil {
		_ = os.Remove(tmp)
		return UpdateOutcome{}, err
	}
	s.version = entry.Version
	return UpdateOutcome{
		Message:     "Rollback completed. Restart TransitMonitor to run the restored version.",
		NeedRestart: true,
		FromVersion: fromVersion,
		ToVersion:   entry.Version,
	}, nil
}

// Restart swaps in the new binary. Bare mode: syscall.Exec (in-place, PID
// preserved) after the pre-restart hook flushes the DB. Docker mode: os.Exit(0)
// and let the container supervisor + wrapper entrypoint re-exec /data/bin.
// Returns immediately; the actual exec/exit happens after a short delay so the
// HTTP response reaches the caller first.
func (s *Service) Restart(ctx context.Context) error {
	if !s.restarting.CompareAndSwap(0, 1) {
		return errors.New("restart already scheduled")
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		if s.preRestart != nil {
			_ = s.preRestart()
		}
		if s.mode == ModeDocker {
			os.Exit(0)
		}
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "updater: restart: %v\n", err)
			os.Exit(1)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		// syscall.Exec replaces this process image in place; the new binary
		// inherits the PID and argv/env. On Linux, renaming over the running
		// binary leaves the old inode mapped, so Exec picks up the new file.
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "updater: exec: %v\n", err)
			os.Exit(1)
		}
	}()
	return nil
}

// ---- semver ----------------------------------------------------------------

// compareVersions returns -1/0/1 for a vs b. Tags may have a leading "v".
// A version containing "dev" is treated as 0.0.0 (always older than a real
// release) so `make build` (default 0.1.0-dev) correctly reports an available
// update instead of claiming to be newer than released tags.
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	if strings.Contains(a, "dev") {
		a = "0.0.0"
	}
	if strings.Contains(b, "dev") {
		b = "0.0.0"
	}
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(s string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(s, ".", 4) {
		if i >= 3 {
			break
		}
		// strip any pre-release suffix like "-rc1"
		if dash := strings.Index(p, "-"); dash >= 0 {
			p = p[:dash]
		}
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// sortedRollbackVersions is a small helper for stable UI ordering.
func sortedRollbackVersions(vs []RollbackVersion) []RollbackVersion {
	cp := append([]RollbackVersion(nil), vs...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].PublishedAt > cp[j].PublishedAt })
	return cp
}
