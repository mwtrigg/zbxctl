package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at build time via -ldflags. A build made with "go install
// module@version" takes the version from the module instead. Anything else is "dev".
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		Version = fromModule(info.Main.Version)
	}
}

// fromModule turns a module version into ours: a release tag ("v1.2.0") gives
// "1.2.0"; a local build ("(devel)") or a commit between releases (a
// pseudo-version, or one marked "+dirty") stays "dev".
func fromModule(v string) string {
	if !strings.HasPrefix(v, "v") || strings.ContainsAny(v, "-+") {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}
