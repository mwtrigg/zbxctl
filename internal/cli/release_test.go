package cli

import (
	"os"
	"strings"
	"testing"
)

// The release workflow must build the tag that was pushed (not the highest tag on the
// commit), must not leave the token in .git/config, and must refuse a tag that is not on
// main. These are easy to lose in an edit and cannot be exercised without a real tag, so
// the text is checked.
func TestReleaseWorkflowKeepsItsGuards(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	w := string(b)
	for _, want := range []string{
		"GORELEASER_CURRENT_TAG: ${{ github.ref_name }}",
		"persist-credentials: false",
		`git merge-base --is-ancestor "$GITHUB_SHA" origin/main`,
		"environment: release",
		"needs: verify",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("release.yml lost %q", want)
		}
	}
	// the signing key must not be in the job that runs the tests
	verify := w[strings.Index(w, "  verify:"):strings.Index(w, "  release:")]
	if strings.Contains(verify, "ZBXCTL_SIGNING_KEY") || strings.Contains(verify, "environment:") {
		t.Error("the verify job must have no secret and no environment")
	}
}
