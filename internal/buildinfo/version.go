// Package buildinfo carries version identifiers shared by release metadata.
package buildinfo

// Version defaults to the development identifier. Release builds override it
// with -ldflags "-X github.com/fleeto/readyspec/internal/buildinfo.Version=X.Y.Z".
var Version = "0.2.0-dev"
