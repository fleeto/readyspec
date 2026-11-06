// Package goss supplies the unmodified, checksum-pinned inspection binaries.
package goss

import (
	"crypto/sha256"
	"embed"
	"fmt"
)

const Version = "v0.4.9"

//go:embed assets/goss-linux-amd64 assets/goss-linux-arm64 assets/LICENSE.goss
var assets embed.FS

var digests = map[string]string{
	"amd64": "87dd36cfa1b8b50554e6e2ca29168272e26755b19ba5438341f7c66b36decc19",
	"arm64": "14fd24ac08236559f4809e6a627792d1b947ed98654bba1662ef1d6122d77e18",
}

// Binary returns a fresh copy so callers cannot mutate the embedded payload.
func Binary(arch string) ([]byte, error) {
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	want, ok := digests[arch]
	if !ok {
		return nil, fmt.Errorf("unsupported Goss architecture")
	}
	raw, err := assets.ReadFile("assets/goss-linux-" + arch)
	if err != nil {
		return nil, fmt.Errorf("embedded Goss missing: %w", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != want {
		return nil, fmt.Errorf("embedded Goss integrity check failed for %s", arch)
	}
	return raw, nil
}

func Verify() error {
	for _, arch := range []string{"amd64", "arm64"} {
		if _, err := Binary(arch); err != nil {
			return err
		}
	}
	return nil
}
