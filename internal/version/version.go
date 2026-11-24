// Package version holds the Cicord build version (overridden with -ldflags at release).
package version

// Version is the engine version, checked against cicord.yaml spec.engine and plugin manifests.
var Version = "0.1.1"

// Commit is the git commit the binary was built from.
var Commit = "dev"
