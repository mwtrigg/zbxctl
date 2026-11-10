package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/output"
	"github.com/mwtrigg/zbxctl/internal/version"
)

var githubReleaseAPI = "https://api.github.com/repos/mwtrigg/zbxctl/releases/latest"

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// errNoRelease means GitHub has no latest (non-prerelease) release for the project.
var errNoRelease = errors.New("no release found")

var upgradeCheckOnly bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Update zbxctl to the latest release",
	RunE:  runUpgrade,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE:  runVersion,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheckOnly, "check", false, "check for updates without installing")
}

func runVersion(_ *cobra.Command, _ []string) error {
	outFmt := output.Format(flagOutput)
	cols := []output.Column{
		{Key: "version", Header: "VERSION"},
		{Key: "os", Header: "OS"},
		{Key: "arch", Header: "ARCH"},
	}
	row := map[string]any{
		"version": version.Version,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}
	if outFmt == output.FormatTable {
		fmt.Println(version.Version)
		return nil
	}
	return output.Print(outFmt, cols, []map[string]any{row})
}

func runUpgrade(_ *cobra.Command, _ []string) error {
	outFmt := output.Format(flagOutput)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	current := version.Version
	rel, err := fetchLatestRelease(ctx)
	if errors.Is(err, errNoRelease) {
		// GitHub's "latest release" ignores prereleases, so with only a release candidate
		// published there is nothing to find. That is not a failure.
		const note = "no final release has been published yet (prereleases are not considered), so there is nothing to upgrade to"
		if outFmt == output.FormatTable {
			fmt.Printf("%s (%s)\n", note, current)
			return nil
		}
		return output.Print(outFmt, []output.Column{{Key: "current_version", Header: "CURRENT"}, {Key: "latest_version", Header: "LATEST"}, {Key: "update_available", Header: "UPDATE_AVAILABLE"}, {Key: "note", Header: "NOTE"}},
			[]map[string]any{{"current_version": current, "latest_version": "", "update_available": false, "note": note}})
	}
	if err != nil {
		return cliPrintErr(outFmt, "failed to fetch release info: "+err.Error(), output.ExitError)
	}

	latest := rel.TagName
	// an update is a different version that is not older than the one running
	updateAvailable := stripV(current) != stripV(latest) && !olderRelease(current, latest)

	if upgradeCheckOnly || !updateAvailable {
		if outFmt == output.FormatTable {
			if updateAvailable {
				fmt.Printf("update available: %s → %s\n", current, latest)
			} else {
				fmt.Printf("up to date (%s)\n", current)
			}
			return nil
		}
		cols := []output.Column{
			{Key: "current_version", Header: "CURRENT"},
			{Key: "latest_version", Header: "LATEST"},
			{Key: "update_available", Header: "UPDATE_AVAILABLE"},
		}
		row := map[string]any{
			"current_version":  current,
			"latest_version":   latest,
			"update_available": updateAvailable,
		}
		return output.Print(outFmt, cols, []map[string]any{row})
	}

	fmt.Fprintf(os.Stderr, "upgrading %s → %s\n", current, latest)

	if olderRelease(current, latest) {
		return cliPrintErr(outFmt, fmt.Sprintf("the latest release (%s) is older than the version running (%s); not downgrading", latest, current), output.ExitError)
	}
	if !flagApply { // --yes sets flagApply too
		// The question goes to stderr and stdout stays clean for the JSON result.
		// With no terminal to answer it, ask for --yes instead of waiting on a pipe.
		if !isTerminal(os.Stdin) {
			return cliPrintErr(outFmt, "upgrade needs your confirmation: run it at a terminal, or pass --apply (or --yes)", output.ExitUsage)
		}
		fmt.Fprintf(os.Stderr, "continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Fprintln(os.Stderr, "cancelled")
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "downloading and verifying %s...\n", archiveName(latest, runtime.GOOS, runtime.GOARCH))
	if err := installLatest(ctx, rel); err != nil {
		return cliPrintErr(outFmt, "upgrade failed: "+err.Error(), output.ExitError)
	}

	if outFmt == output.FormatTable {
		fmt.Printf("upgraded to %s\n", latest)
		return nil
	}
	cols := []output.Column{
		{Key: "current_version", Header: "PREVIOUS"},
		{Key: "latest_version", Header: "INSTALLED"},
		{Key: "updated", Header: "UPDATED"},
	}
	row := map[string]any{
		"current_version": current,
		"latest_version":  latest,
		"updated":         true,
	}
	return output.Print(outFmt, cols, []map[string]any{row})
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleaseAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zbxctl/"+version.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func downloadAsset(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "zbxctl/"+version.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	_, err = io.Copy(w, resp.Body)
	return err
}

func stripV(v string) string {
	return strings.TrimPrefix(v, "v")
}
