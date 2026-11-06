//go:build linux

package compiler_test

import (
	"fmt"
	"testing"
)

func TestArchitectureCollectorPreservesRawAndNormalizedEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, output, normalized, status string
		exit                             int
	}{
		{"amd64_alias", "amd64\n", "x86_64", "PASS", 0},
		{"arm64_alias", "arm64\n", "aarch64", "PASS", 0},
		{"canonical_x86", "x86_64\n", "x86_64", "PASS", 0},
		{"canonical_arm", "aarch64\n", "aarch64", "PASS", 0},
		{"without_newline", "arm64", "aarch64", "PASS", 0},
		{"unknown_architecture", "riscv64\n", "riscv64", "FAIL", 0},
		{"empty_output", "", "", "ERROR", 0},
		{"multiple_lines", "amd64\narm64\n", "", "ERROR", 0},
		{"extra_blank_line", "amd64\n\n", "", "ERROR", 0},
		{"whitespace_output", " \t\n", "", "ERROR", 0},
		{"embedded_whitespace", "x86 64\n", "", "ERROR", 0},
		{"nul_output", "amd64\x00other\n", "", "ERROR", 0},
		{"query_failed", "amd64\n", "x86_64", "ERROR", 2},
		{"missing_tool", "", "", "ERROR", 127},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "uname-output", tt.output)
			fixtureTool(t, root, "tools/uname", fmt.Sprintf(`[ "$*" = '-m' ] || exit 99; cat /uname-output; exit %d`, tt.exit))
			artifact := collectionArtifact(t, "H01", map[string]any{"allowed_arch": []any{"x86_64", "aarch64"}})
			results := collectInSandbox(t, root, artifact)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if raw := evidenceProperty(t, results, "uname_raw"); raw != tt.output {
				t.Fatalf("uname raw output changed: want %q got %q", tt.output, raw)
			}
			if normalized := evidenceProperty(t, results, "normalized_architecture"); normalized != tt.normalized {
				t.Fatalf("want architecture %q got %v", tt.normalized, normalized)
			}
			if tt.exit != 0 && fmt.Sprint(evidenceProperty(t, results, "exit-status")) != fmt.Sprint(tt.exit) {
				t.Fatal("query failure status lost")
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "uname -m" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("architecture observation source or times lost")
				}
			}
			if tt.status == "ERROR" && tt.exit == 0 && evidenceProperty(t, results, "error") != "INVALID_UNAME_OUTPUT" {
				t.Fatal("malformed output was not classified explicitly")
			}
		})
	}
}
