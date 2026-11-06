//go:build linux

package compiler_test

import (
	"testing"
)

func dpkgStatus(pkg, status, version string) string {
	return "Package: " + pkg + "\nStatus: " + status + "\nArchitecture: amd64\nVersion: " + version + "\nMaintainer: fixture <fixture@example.invalid>\nDescription: fixture package\n\n"
}

func TestPackageCollectorComparesRealDpkgVersions(t *testing.T) {
	for _, tt := range []struct {
		name, status, version, minimum, maximum, expect string
		installed                                       bool
	}{
		{"epoch_release_pass", "install ok installed", "1:2.10-1", "1:2.9-1", "1:2.11-1", "PASS", true},
		{"tilde_sorts_lower", "install ok installed", "1.0~rc1-1", "1.0-1", "", "FAIL", true},
		{"above_maximum", "install ok installed", "1:2.12-1", "1:2.9-1", "1:2.11-1", "FAIL", true},
		{"uninstalled_expected", "deinstall ok config-files", "1:2.10-1", "1:2.9-1", "", "FAIL", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "var/lib/dpkg/status", dpkgStatus("fixture-app", tt.status, tt.version))
			params := map[string]any{"name": "fixture-app", "manager": "dpkg", "installed": tt.installed, "minimum_version": tt.minimum}
			if tt.maximum != "" {
				params["maximum_version"] = tt.maximum
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "D01", params))
			if len(results) != 1 || results[0].Status != tt.expect {
				t.Fatalf("want %s got %+v", tt.expect, results)
			}
		})
	}
}

func TestPackageCollectorKeepsRealDpkgWarningsOutOfData(t *testing.T) {
	root := collectionRoot(t)
	fixtureFile(t, root, "var/lib/dpkg/status", dpkgStatus("fixture-app", "install ok installed", "1:2.10-1")+"Package: broken\nStatus: install ok installed\nVersion: 1\n\n")
	results := collectInSandbox(t, root, collectionArtifact(t, "D01", map[string]any{"name": "fixture-app", "manager": "dpkg", "installed": true, "minimum_version": "1:2.9-1"}))
	if len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("parser warnings must not be read as package state: %+v", results)
	}
}

func TestPackageCollectorReportsRealDpkgAbsence(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installed bool
		expect    string
	}{
		{"absent_expected_false", false, "PASS"},
		{"absent_expected_true", true, "FAIL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "var/lib/dpkg/status", dpkgStatus("other-app", "install ok installed", "1.0-1"))
			results := collectInSandbox(t, root, collectionArtifact(t, "D01", map[string]any{"name": "fixture-app", "manager": "dpkg", "installed": tt.installed}))
			if len(results) != 1 || results[0].Status != tt.expect {
				t.Fatalf("want %s got %+v", tt.expect, results)
			}
		})
	}
}

func TestPackageCollectorSeparatesRpmDiagnosticsFromVersion(t *testing.T) {
	for _, tt := range []struct {
		name, script, expect string
		installed            bool
	}{
		{"uninstalled", `printf 'package fixture-app is not installed\n' >&2; exit 1`, "PASS", false},
		{"uninstalled_expected_installed", `printf 'package fixture-app is not installed\n' >&2; exit 1`, "FAIL", true},
		{"lock_error", `printf 'error: can\'t create transaction lock on /var/lib/rpm/.rpm.lock\n' >&2; exit 1`, "ERROR", false},
		{"warning_on_success", `case "$1" in --eval) printf 'warning: fixture db notice\n' >&2; printf '1';; *) printf 'warning: fixture db notice\n' >&2; printf '0:2.10-1';; esac`, "PASS", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureTool(t, root, "tools/rpm", tt.script)
			results := collectInSandbox(t, root, collectionArtifact(t, "D01", map[string]any{"name": "fixture-app", "manager": "rpm", "installed": tt.installed, "minimum_version": "0:1.0-1"}))
			if len(results) != 1 || results[0].Status != tt.expect {
				t.Fatalf("want %s got %+v", tt.expect, results)
			}
		})
	}
}
