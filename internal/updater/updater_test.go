package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.0.1", "v0.0.2", -1},
		{"v0.0.2", "v0.0.1", 1},
		{"0.0.1", "0.0.1", 0},
		{"0.1.0", "0.0.9", 1},
		{"1.2.3", "1.2.4", -1},
		// a dev build is always older than any real release
		{"0.1.0-dev", "0.0.1", -1},
		{"0.1.0-dev", "v0.0.2", -1},
		{"0.0.1", "0.1.0-dev", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestValidateDownloadURL(t *testing.T) {
	good := []string{
		"https://github.com/yang-yang9/TransitMonitor/releases/download/v0.0.2/transitmonitor_linux_amd64.tar.gz",
		"https://objects.githubusercontent.com/y/abc",
		"https://codeload.github.com/x",
		"https://release-assets.githubusercontent.com/y",
	}
	for _, u := range good {
		if err := validateDownloadURL(u); err != nil {
			t.Errorf("expected %q to be allowed, got %v", u, err)
		}
	}
	bad := []string{
		"http://github.com/x",               // not https
		"https://evil.example.com/x",        // wrong host
		"https://api.github.com.evil.com/x", // not a githubusercontent subdomain
		"file:///etc/passwd",
	}
	for _, u := range bad {
		if err := validateDownloadURL(u); err == nil {
			t.Errorf("expected %q to be rejected", u)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("hello transitmonitor")
	path := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := "2a9e3a3fb4e0c7b3f7d3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
	// wrong checksum → mismatch
	if err := verifyChecksum("deadbeef archive.tar.gz\n", "archive.tar.gz", path); err == nil {
		t.Fatal("expected mismatch error")
	}
	// real checksum → ok (compute it)
	real, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	checksums := real + "  archive.tar.gz\n"
	if err := verifyChecksum(checksums, "archive.tar.gz", path); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	// missing entry
	if err := verifyChecksum("00 other.tar.gz\n", "archive.tar.gz", path); err == nil {
		t.Fatal("expected missing-entry error")
	}
	_ = sum
}

// makeArchive builds a gzip+tar whose entries are the given (name, content)
// pairs (all regular files).
func makeArchive(t *testing.T, entries []struct{ name, body string }) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	archiveBytes := makeArchive(t, []struct{ name, body string }{
		{"README.md", "docs"},
		{"transitmonitor", "#!/bin/sh\nrun"},
		{"sub/dir/transitmonitor.exe", "win"},
	})
	archivePath := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := extractBinary(archivePath, out); err != nil {
		t.Fatalf("extract: %v", err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "#!/bin/sh\nrun" {
		t.Fatalf("unexpected body %q", string(b))
	}
}

func TestExtractBinaryZipSlip(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// a transitmonitor-named entry that escapes the base dir via ..
	hdr := &tar.Header{Name: "../../etc/transitmonitor", Mode: 0o755, Size: 1, Typeflag: tar.TypeReg}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	archivePath := filepath.Join(dir, "evil.tar.gz")
	_ = os.WriteFile(archivePath, buf.Bytes(), 0o644)
	out := filepath.Join(dir, "out")
	if err := extractBinary(archivePath, out); err == nil {
		t.Fatal("expected Zip-Slip rejection, got nil")
	} else if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("expected refused error, got %v", err)
	}
}

func TestPickAssets(t *testing.T) {
	want := runtime.GOOS + "_" + runtime.GOARCH
	assets := []ghAsset{
		{Name: "transitmonitor_linux_amd64.tar.gz", BrowserDownloadURL: "https://github.com/x/transitmonitor_linux_amd64.tar.gz"},
		{Name: "transitmonitor_darwin_arm64.tar.gz", BrowserDownloadURL: "https://github.com/x/transitmonitor_darwin_arm64.tar.gz"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://github.com/x/checksums.txt"},
		{Name: "README.txt", BrowserDownloadURL: "https://github.com/x/README.txt"},
	}
	archive, sums := pickAssets(assets)
	if archive == nil {
		t.Fatal("archive not picked")
	}
	if !strings.Contains(archive.Name, want) {
		t.Fatalf("picked wrong archive %q for %s", archive.Name, want)
	}
	if sums == nil || sums.Name != "checksums.txt" {
		t.Fatalf("checksums not picked: %+v", sums)
	}
}

// TestArchiveAndRollback exercises the manifest round-trip + RollbackToVersion
// against a bare-mode Service with a fake "running" binary.
func TestArchiveAndRollback(t *testing.T) {
	dir := t.TempDir()
	// fake running binary
	exe := filepath.Join(dir, "transitmonitor")
	original := []byte("BINARY-v0.0.1")
	if err := os.WriteFile(exe, original, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Service{
		version: "0.0.1",
		repo:    "yang-yang9/TransitMonitor",
		mode:    ModeBare,
		exePath: exe,
		dataDir: dir,
	}
	// archive a couple of "prior" binaries as backups
	v2Bin := filepath.Join(dir, "0.0.2.bin")
	if err := os.WriteFile(v2Bin, []byte("BINARY-v0.0.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.archiveBackup(v2Bin, "0.0.2"); err != nil {
		t.Fatal(err)
	}
	v3Bin := filepath.Join(dir, "0.0.3.bin")
	if err := os.WriteFile(v3Bin, []byte("BINARY-v0.0.3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.archiveBackup(v3Bin, "0.0.3"); err != nil {
		t.Fatal(err)
	}
	vers, err := s.ListRollbackVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vers) != 2 {
		t.Fatalf("expected 2 rollback versions, got %d", len(vers))
	}
	// Roll back to 0.0.2 → active binary should now be the v0.0.2 bytes.
	out, err := s.RollbackToVersion(context.Background(), "0.0.2")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if out.ToVersion != "0.0.2" || !out.NeedRestart {
		t.Fatalf("unexpected outcome %+v", out)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "BINARY-v0.0.2" {
		t.Fatalf("active binary not swapped: %q", string(got))
	}
}

func TestRetentionCap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "transitmonitor")
	_ = os.WriteFile(exe, []byte("cur"), 0o755)
	s := &Service{version: "0.0.1", mode: ModeBare, exePath: exe, dataDir: dir}
	for i := 0; i < maxRollbackVersions+2; i++ {
		v := filepath.Join(dir, "b.bin")
		_ = os.WriteFile(v, []byte("b"), 0o755)
		if err := s.archiveBackup(v, "0.0."+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	m := s.loadManifest()
	if len(m.Backups) != maxRollbackVersions {
		t.Fatalf("expected %d retained, got %d", maxRollbackVersions, len(m.Backups))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
