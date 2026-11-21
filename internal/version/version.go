// Package version holds the CODI build version (overridden with -ldflags at release).
package version

// Version is the engine version, checked against codi.yaml spec.engine and plugin manifests.
var Version = "0.1.0"

// Commit is the git commit the binary was built from.
var Commit = "dev"
