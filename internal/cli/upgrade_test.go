package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func TestInstallLatestVerifiesBeforeInstalling(t *testing.T) {
	name := archiveName(runtime.GOOS, runtime.GOARCH)
	var archive []byte
	if runtime.GOOS == "windows" {
		archive = makeZip(t, map[string]string{binaryName(runtime.GOOS): "NEWBINARY"})
	} else {
		archive = makeTarGz(t, map[string]string{binaryName(runtime.GOOS): "NEWBINARY"})
	}
	sums := fmt.Sprintf("%s  %s\n", sha(archive), name)
	body := map[string][]byte{"/archive": archive, "/sums": []byte(sums)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body[r.URL.Path]) }))
	defer srv.Close()
	rel := &githubRelease{TagName: "v9", Assets: []githubAsset{{Name: name, BrowserDownloadURL: srv.URL + "/archive"}, {Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/sums"}}}

	var installed string
	old := installBinary
	defer func() { installBinary = old }()
	installBinary = func(b []byte) error { installed = string(b); return nil }

	if err := installLatest(context.Background(), rel); err != nil || installed != "NEWBINARY" {
		t.Fatalf("good release: err=%v installed=%q", err, installed)
	}

	installed = ""
	body["/archive"] = append([]byte{}, archive...)
	body["/archive"][len(archive)/2] ^= 0xff // corrupt one byte
	if err := installLatest(context.Background(), rel); err == nil || installed != "" {
		t.Fatalf("a corrupted download must not be installed: err=%v installed=%q", err, installed)
	}
	body["/archive"] = archive

	noSums := &githubRelease{TagName: "v9", Assets: rel.Assets[:1]}
	if err := installLatest(context.Background(), noSums); err == nil || installed != "" || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("a release without checksums.txt must not be installed: err=%v", err)
	}
	noArchive := &githubRelease{TagName: "v9", Assets: rel.Assets[1:]}
	if err := installLatest(context.Background(), noArchive); err == nil || installed != "" {
		t.Fatalf("a release without an asset for this platform: err=%v", err)
	}
}
