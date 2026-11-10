package cli

import (
	"crypto/ed25519"
	"os"
	"path/filepath"

	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/mwtrigg/zbxctl/internal/sshsig"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestArchiveNamesMatchWhatTheReleaseProduces(t *testing.T) {
	if archiveName("linux", "amd64") != "zbxctl_linux_amd64.tar.gz" || archiveName("darwin", "arm64") != "zbxctl_darwin_arm64.tar.gz" || archiveName("windows", "amd64") != "zbxctl_windows_amd64.zip" {
		t.Fatal("archive names changed: .goreleaser.yaml must change with them")
	}
	if binaryName("windows") != "zbxctl.exe" || binaryName("linux") != "zbxctl" {
		t.Fatal("binary names")
	}
}

func TestChecksumVerification(t *testing.T) {
	data := []byte("archive bytes")
	sums := fmt.Sprintf("%s  zbxctl_linux_amd64.tar.gz\n%s  other.tar.gz\n", sha(data), sha([]byte("x")))
	if err := verifyChecksum(data, sums, "zbxctl_linux_amd64.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum([]byte("tampered"), sums, "zbxctl_linux_amd64.tar.gz"); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("a changed download must fail: %v", err)
	}
	if err := verifyChecksum(data, sums, "zbxctl_freebsd_amd64.tar.gz"); err == nil {
		t.Fatal("an asset missing from checksums.txt must fail")
	}
	if _, ok := checksumFor("ABC123 *zbxctl.zip", "zbxctl.zip"); !ok {
		t.Fatal("sha256sum's binary-mode marker must be accepted")
	}
}

func TestExtractBinaryFromBothArchiveTypes(t *testing.T) {
	files := map[string]string{"zbxctl": "BINARY", "LICENSE": "MIT", "THIRD_PARTY_NOTICES.md": "notices"}
	if b, err := extractBinary("a.tar.gz", makeTarGz(t, files), "zbxctl"); err != nil || string(b) != "BINARY" {
		t.Fatalf("tar.gz: %q %v", b, err)
	}
	zfiles := map[string]string{"zbxctl.exe": "EXE", "LICENSE": "MIT"}
	if b, err := extractBinary("a.zip", makeZip(t, zfiles), "zbxctl.exe"); err != nil || string(b) != "EXE" {
		t.Fatalf("zip: %q %v", b, err)
	}
	if _, err := extractBinary("a.tar.gz", makeTarGz(t, map[string]string{"LICENSE": "x"}), "zbxctl"); err == nil {
		t.Fatal("an archive without the binary must fail")
	}
	if _, err := extractBinary("a.rar", nil, "zbxctl"); err == nil {
		t.Fatal("unknown archive types must fail")
	}
}

// releaseFixture serves a signed release for this platform and returns it with
// a handle to tamper with what is served.
type releaseFixture struct {
	rel     *githubRelease
	body    map[string][]byte
	archive []byte
	pubLine string
	priv    ed25519.PrivateKey
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	name := archiveName(runtime.GOOS, runtime.GOARCH)
	var archive []byte
	if runtime.GOOS == "windows" {
		archive = makeZip(t, map[string]string{binaryName(runtime.GOOS): "NEWBINARY"})
	} else {
		archive = makeTarGz(t, map[string]string{binaryName(runtime.GOOS): "NEWBINARY"})
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	sums := []byte(fmt.Sprintf("%s  %s\n", sha(archive), name))
	f := &releaseFixture{archive: archive, priv: priv, pubLine: sshsig.PublicKeyLine(pub, "test release key"),
		body: map[string][]byte{"/archive": archive, "/sums": sums, "/sig": sshsig.SignEd25519(priv, sums, signatureNamespace)}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(f.body[r.URL.Path]) }))
	t.Cleanup(srv.Close)
	oldPrefix, oldKeys := releaseURLPrefix, releaseKeys
	releaseURLPrefix, releaseKeys = srv.URL+"/", []string{f.pubLine}
	t.Cleanup(func() { releaseURLPrefix, releaseKeys = oldPrefix, oldKeys })
	f.rel = &githubRelease{TagName: "v9", Assets: []githubAsset{
		{Name: name, BrowserDownloadURL: srv.URL + "/archive"},
		{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/sums"},
		{Name: "checksums.txt.sig", BrowserDownloadURL: srv.URL + "/sig"},
	}}
	return f
}

func TestInstallLatestVerifiesSignatureAndChecksumBeforeInstalling(t *testing.T) {
	f := newReleaseFixture(t)
	var installed string
	old := installBinary
	defer func() { installBinary = old }()
	installBinary = func(b []byte) error { installed = string(b); return nil }
	try := func(rel *githubRelease) error { installed = ""; return installLatest(context.Background(), rel) }

	if err := try(f.rel); err != nil || installed != "NEWBINARY" {
		t.Fatalf("a properly signed release: err=%v installed=%q", err, installed)
	}

	// archive corrupted after signing
	good := f.body["/archive"]
	f.body["/archive"] = append([]byte{}, good...)
	f.body["/archive"][len(good)/2] ^= 0xff
	if err := try(f.rel); err == nil || installed != "" || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("a corrupted archive must not be installed: err=%v", err)
	}
	f.body["/archive"] = good

	// checksums.txt replaced (an attacker also fixes the hash) but the signature no longer matches
	evil := []byte(fmt.Sprintf("%s  %s\n", sha([]byte("evil")), archiveName(runtime.GOOS, runtime.GOARCH)))
	goodSums := f.body["/sums"]
	f.body["/sums"] = evil
	if err := try(f.rel); err == nil || installed != "" || !strings.Contains(err.Error(), "signed") {
		t.Fatalf("altered checksums must fail the signature check: err=%v", err)
	}
	f.body["/sums"] = goodSums

	// signed by someone else
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	f.body["/sig"] = sshsig.SignEd25519(otherPriv, goodSums, signatureNamespace)
	if err := try(f.rel); err == nil || installed != "" {
		t.Fatalf("a signature from an untrusted key must fail: err=%v", err)
	}
	f.body["/sig"] = sshsig.SignEd25519(f.priv, goodSums, "something-else")
	if err := try(f.rel); err == nil || installed != "" {
		t.Fatalf("a signature in another namespace must fail: err=%v", err)
	}
	f.body["/sig"] = sshsig.SignEd25519(f.priv, goodSums, signatureNamespace)

	// missing pieces
	if err := try(&githubRelease{TagName: "v9", Assets: f.rel.Assets[:2]}); err == nil || installed != "" || !strings.Contains(err.Error(), "signed checksums") {
		t.Fatalf("a release without a signature must not be installed: err=%v", err)
	}
	if err := try(&githubRelease{TagName: "v9", Assets: f.rel.Assets[1:]}); err == nil || installed != "" {
		t.Fatalf("no asset for this platform: err=%v", err)
	}

	// no key compiled in
	releaseKeys = nil
	if err := try(f.rel); err == nil || installed != "" || !strings.Contains(err.Error(), "no release signing key") {
		t.Fatalf("a build without a release key must not install anything: err=%v", err)
	}
	releaseKeys = []string{f.pubLine}

	// downloads only from the release host
	foreign := &githubRelease{TagName: "v9", Assets: []githubAsset{
		{Name: f.rel.Assets[0].Name, BrowserDownloadURL: "https://evil.example/archive"},
		f.rel.Assets[1], f.rel.Assets[2]}}
	if err := try(foreign); err == nil || installed != "" || !strings.Contains(err.Error(), "refusing to download") {
		t.Fatalf("a download URL outside the release must be refused: err=%v", err)
	}
}

func TestReplaceFileKeepsABackupAndRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "zbxctl")
	os.WriteFile(target, []byte("OLD"), 0o755)

	if err := replaceFile(target, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "NEW" {
		t.Fatalf("not replaced: %q", b)
	}
	if b, _ := os.ReadFile(target + ".old"); string(b) != "OLD" {
		t.Fatalf("the old binary must be kept as .old: %q", b)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("the new binary must be executable")
	}

	// a swap that fails halfway must leave the working binary in place
	calls := 0
	renameFn = func(a, b string) error {
		calls++
		if calls == 2 {
			return fmt.Errorf("disk says no")
		}
		return os.Rename(a, b)
	}
	defer func() { renameFn = os.Rename }()
	if err := replaceFile(target, []byte("NEWER")); err == nil {
		t.Fatal("the injected failure must surface")
	}
	if b, _ := os.ReadFile(target); string(b) != "NEW" {
		t.Fatalf("the previous binary must be restored, got %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".zbxctl-upgrade-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestNeverDowngrades(t *testing.T) {
	for cur, c := range map[string]struct {
		latest string
		older  bool
	}{"0.2.0": {"v0.1.9", true}, "v1.0.0": {"1.0.0", false}, "0.1.0": {"v0.2.0", false}, "1.2.3": {"1.10.0", false}, "dev": {"v0.1.0", false}, "0.0.0-SNAPSHOT-abc": {"v0.0.1", false}} {
		if olderRelease(cur, c.latest) != c.older {
			t.Errorf("current %s, latest %s: older=%v want %v", cur, c.latest, !c.older, c.older)
		}
	}
}
