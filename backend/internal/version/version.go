// Package version carries the build version, stamped at link time.
package version

// Version is overwritten via -ldflags at build time; "dev" is what a local
// `go run` reports.
var Version = "dev"
