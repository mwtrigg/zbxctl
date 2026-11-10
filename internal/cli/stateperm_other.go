//go:build !unix

package cli

// checkStateDir has nothing to check where there are no Unix owners and modes; the
// per-user profile directory is the protection there.
func checkStateDir(string) error { return nil }
