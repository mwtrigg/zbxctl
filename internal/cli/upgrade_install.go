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
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mwtrigg/zbxctl/internal/sshsig"
	"github.com/mwtrigg/zbxctl/internal/version"
)

// Release archives are named zbxctl_<version>_<os>_<arch>.tar.gz (zip on Windows) and are
// listed, with their sha256, in the release's checksums.txt.

const maxDownload = 200 << 20 // a zbxctl archive is a few MB; refuse anything absurd

// signatureNamespace is the SSH signature namespace releases are signed under
// (ssh-keygen -Y sign -n zbxctl-release).
const signatureNamespace = "zbxctl-release"

// releaseURLPrefix is the only place release files are downloaded from,
// whatever the release API answers.
var releaseURLPrefix = "https://github.com/mwtrigg/zbxctl/releases/download/"

// archiveName is the release asset for a version and platform. The version is in
// the name on purpose: checksums.txt is signed, so the signature covers which
// version each archive is. Without it the same file names appear in every
// release, and anyone who can publish a release could attach an old, signed,
// vulnerable build under a new tag and have it accepted as an upgrade.
func archiveName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("zbxctl_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "zbxctl.exe"
	}
	return "zbxctl"
}

// checksumFor finds the sha256 of an asset in the text of a checksums.txt
// ("<hex>  <name>" per line, as `sha256sum` and GoReleaser write it).
func checksumFor(checksums, name string) (string, bool) {
	for _, line := range strings.Split(checksums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// verifyChecksum checks data against the checksum listed for name.
func verifyChecksum(data []byte, checksums, name string) error {
	want, ok := checksumFor(checksums, name)
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: the download is %s, the release lists %s", name, got, want)
	}
	return nil
}

// extractBinary pulls the zbxctl executable out of a .tar.gz or .zip archive.
func extractBinary(archive string, data []byte, want string) ([]byte, error) {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == want && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxDownload))
			}
		}
	case strings.HasSuffix(archive, ".tar.gz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == want {
				return io.ReadAll(io.LimitReader(tr, maxDownload))
			}
		}
	default:
		return nil, fmt.Errorf("unknown archive type %s", archive)
	}
	return nil, fmt.Errorf("%s not found in %s", want, archive)
}

func fetchBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := downloadAsset(ctx, url, &limitedWriter{w: &buf, n: limit}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type limitedWriter struct {
	w io.Writer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, fmt.Errorf("download is larger than %d bytes", maxDownload)
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

// installLatest downloads the archive for this platform and installs it only if
// it can be verified end to end: checksums.txt must carry a valid signature from
// one of the release keys compiled into this binary, and the archive must match
// its entry in checksums.txt. It refuses to install anything it cannot verify.
func installLatest(ctx context.Context, rel *githubRelease) error {
	if len(releaseKeys) == 0 {
		return fmt.Errorf("this build has no release signing key compiled in, so downloads cannot be verified; update by hand (see the README)")
	}
	if _, ok := semverParts(rel.TagName); !ok {
		return fmt.Errorf("the release tag %q is not a version number; not installing", rel.TagName)
	}
	if olderRelease(version.Version, rel.TagName) {
		return fmt.Errorf("the release (%s) is older than the version running (%s); not downgrading", rel.TagName, version.Version)
	}
	want := archiveName(rel.TagName, runtime.GOOS, runtime.GOARCH)
	var archiveURL, sumsURL, sigURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			archiveURL = a.BrowserDownloadURL
		case "checksums.txt":
			sumsURL = a.BrowserDownloadURL
		case "checksums.txt.sig":
			sigURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return fmt.Errorf("no release asset %s for %s/%s", want, runtime.GOOS, runtime.GOARCH)
	}
	if sumsURL == "" || sigURL == "" {
		return fmt.Errorf("the release has no signed checksums (checksums.txt and checksums.txt.sig), so the download cannot be verified; not installing")
	}
	for _, u := range []string{archiveURL, sumsURL, sigURL} {
		if !strings.HasPrefix(u, releaseURLPrefix) {
			return fmt.Errorf("refusing to download from %s: release files must come from %s", u, releaseURLPrefix)
		}
	}
	sums, err := fetchBytes(ctx, sumsURL, 1<<20)
	if err != nil {
		return fmt.Errorf("downloading checksums.txt: %w", err)
	}
	sig, err := fetchBytes(ctx, sigURL, 1<<20)
	if err != nil {
		return fmt.Errorf("downloading checksums.txt.sig: %w", err)
	}
	if err := sshsig.Verify(sig, sums, signatureNamespace, releaseKeys); err != nil {
		return fmt.Errorf("the release's checksums.txt is not signed by a trusted release key: %w", err)
	}
	archive, err := fetchBytes(ctx, archiveURL, maxDownload)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", want, err)
	}
	// The signed checksums must list this very archive name, which carries the
	// version: a replayed older release lists its own version's name instead.
	if _, ok := checksumFor(string(sums), want); !ok {
		return fmt.Errorf("the signed checksums do not list %s, so this release is not what its tag says; not installing", want)
	}
	if err := verifyChecksum(archive, string(sums), want); err != nil {
		return err
	}
	bin, err := extractBinary(want, archive, binaryName(runtime.GOOS))
	if err != nil {
		return err
	}
	return installBinary(bin)
}

// installBinary replaces the running executable; tests swap it out.
var installBinary = replaceExecutable

// replaceExecutable swaps the running executable for new content. The old
// binary is kept next to it as zbxctl.old so a bad update can be undone, and is
// put back if the swap fails.
func replaceExecutable(content []byte) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine the binary path: %w", err)
	}
	if execPath, err = filepath.EvalSymlinks(execPath); err != nil {
		return fmt.Errorf("could not resolve the binary path: %w", err)
	}
	return replaceFile(execPath, content)
}

// renameFn is os.Rename; tests replace it to simulate a failing swap.
var renameFn = os.Rename

// replaceFile is replaceExecutable for an explicit path.
func replaceFile(execPath string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(execPath), ".zbxctl-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s (%v); download the release and install it by hand instead (see the README)", execPath, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	old := execPath + ".old"
	_ = os.Remove(old)
	if err := renameFn(execPath, old); err != nil {
		return fmt.Errorf("could not move the current binary aside: %w", err)
	}
	if err := renameFn(tmpPath, execPath); err != nil {
		_ = os.Rename(old, execPath) // put the working binary back
		return fmt.Errorf("could not install the new binary (the old one was restored): %w", err)
	}
	return nil
}

// olderRelease reports whether latest is a lower version than current. Versions
// that do not parse (a development build) are never considered older.
func olderRelease(current, latest string) bool {
	c, cok := semverParts(current)
	l, lok := semverParts(latest)
	if !cok || !lok {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] < c[i]
		}
	}
	return false
}

func semverParts(v string) ([3]int, bool) {
	v = strings.TrimPrefix(v, "v")
	var out [3]int
	parts := strings.SplitN(v, "-", 2)
	nums := strings.Split(parts[0], ".")
	if len(nums) != 3 {
		return out, false
	}
	for i, n := range nums {
		if _, err := fmt.Sscanf(n, "%d", &out[i]); err != nil {
			return out, false
		}
	}
	return out, true
}
