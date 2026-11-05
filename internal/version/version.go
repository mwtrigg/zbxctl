package version

// Version is set at build time via -ldflags. Falls back to "dev" for local builds.
var Version = "dev"
