package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	rel, err := fetchLatestRelease(ctx)
	if err != nil {
		return cliPrintErr(outFmt, "failed to fetch release info: "+err.Error(), output.ExitError)
	}

	current := version.Version
	latest := rel.TagName
	updateAvailable := stripV(current) != stripV(latest)

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

	if outFmt == output.FormatTable {
		fmt.Printf("upgrading %s → %s\n", current, latest)
	}

	if !flagYes {
		fmt.Printf("continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Println("cancelled")
			return nil
		}
	}

	if olderRelease(current, latest) {
		return cliPrintErr(outFmt, fmt.Sprintf("the latest release (%s) is older than the version running (%s); not downgrading", latest, current), output.ExitError)
	}
	if outFmt == output.FormatTable {
		fmt.Printf("downloading and verifying %s...\n", archiveName(latest, runtime.GOOS, runtime.GOARCH))
	}
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
