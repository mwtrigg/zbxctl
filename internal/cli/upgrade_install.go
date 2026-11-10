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
)

// Release archives are named zbxctl_<os>_<arch>.tar.gz (zip on Windows) and are
// listed, with their sha256, in the release's checksums.txt.

const maxDownload = 200 << 20 // a zbxctl archive is a few MB; refuse anything absurd

// archiveName is the release asset for a platform.
func archiveName(goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("zbxctl_%s_%s%s", goos, goarch, ext)
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

// installLatest downloads the archive for this platform, verifies it against the
// release's checksums.txt, and replaces the running executable. It refuses to
// install anything it cannot verify.
func installLatest(ctx context.Context, rel *githubRelease) error {
	want := archiveName(runtime.GOOS, runtime.GOARCH)
	var archiveURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			archiveURL = a.BrowserDownloadURL
		case "checksums.txt":
			sumsURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return fmt.Errorf("no release asset %s for %s/%s", want, runtime.GOOS, runtime.GOARCH)
	}
	if sumsURL == "" {
		return fmt.Errorf("the release has no checksums.txt, so the download cannot be verified; not installing")
	}
	sums, err := fetchBytes(ctx, sumsURL, 1<<20)
	if err != nil {
		return fmt.Errorf("downloading checksums.txt: %w", err)
	}
	archive, err := fetchBytes(ctx, archiveURL, maxDownload)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", want, err)
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

// replaceExecutable swaps the running executable for new content.
func replaceExecutable(content []byte) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine the binary path: %w", err)
	}
	if execPath, err = filepath.EvalSymlinks(execPath); err != nil {
		return fmt.Errorf("could not resolve the binary path: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(execPath), ".zbxctl-upgrade-*")
	if err != nil {
		return fmt.Errorf("could not create a temporary file next to the binary: %w", err)
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
	if runtime.GOOS == "windows" { // a running .exe cannot be overwritten, but it can be renamed
		old := execPath + ".old"
		_ = os.Remove(old)
		if err := os.Rename(execPath, old); err != nil {
			return fmt.Errorf("could not move the running binary aside: %w", err)
		}
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("could not replace the binary: %w", err)
	}
	return nil
}
