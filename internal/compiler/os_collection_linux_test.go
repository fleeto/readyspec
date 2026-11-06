//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOSReleaseCollectorParsesSourceAndVersionBoundaries(t *testing.T) {
	for _, tt := range []struct{ name, content, minimum, maximum, status, version string }{
		{"unquoted", "ID=debian\nVERSION_ID=12.1\n", "12.1", "12.1", "PASS", "12.1"},
		{"single_quoted", "ID='debian'\nVERSION_ID='12.1'\n", "12.1", "12.1", "PASS", "12.1"},
		{"double_quoted", "ID=\"debian\"\nVERSION_ID=\"12.1\"\n", "12.1", "12.1", "PASS", "12.1"},
		{"shell_escaped_unquoted_version", "ID=debian\nVERSION_ID=12\\.1\n", "12.1", "12.1", "PASS", "12.1"},
		{"shell_escaped_metadata", "ID=debian\nVERSION_ID=12.1\nNAME=\"Ready \\$HOME \\`id\\` \\\\ \\\"Linux\\\"\"\n", "12", "13", "PASS", "12.1"},
		{"literal_unknown_double_escape", "ID=debian\nVERSION_ID=12.1\nNAME=\"Linux \\q\"\n", "12", "13", "PASS", "12.1"},
		{"comment_and_spacing", " # comment\n ID=debian  # ID comment\nVERSION_ID=\"12.1\" # version comment\n", "12", "13", "PASS", "12.1"},
		{"duplicate_id", "ID=debian\nID=debian\nVERSION_ID=12\n", "", "", "ERROR", ""},
		{"duplicate_version", "ID=debian\nVERSION_ID=12\nVERSION_ID=13\n", "", "", "ERROR", ""},
		{"missing_id", "VERSION_ID=12\n", "", "", "ERROR", ""},
		{"missing_version", "ID=debian\n", "", "", "ERROR", ""},
		{"empty_id", "ID=\"\"\nVERSION_ID=12\n", "", "", "ERROR", ""},
		{"empty_version", "ID=debian\nVERSION_ID=\"\"\n", "", "", "ERROR", ""},
		{"unclosed_single_quote", "ID='debian\nVERSION_ID=12\n", "", "", "ERROR", ""},
		{"unclosed_double_quote", "ID=debian\nVERSION_ID=\"12\n", "", "", "ERROR", ""},
		{"concatenated_quotes", "ID='debian''\nVERSION_ID=12\n", "", "", "ERROR", ""},
		{"go_hex_escape_is_not_shell_escape", "ID=debian\nVERSION_ID=\"12\\x2e1\"\n", "12.1", "12.1", "ERROR", ""},
		{"nul_field", "ID=debian\nVERSION_ID=12\x00\n", "", "", "ERROR", ""},
		{"unknown_distribution", "ID=unknown_linux\nVERSION_ID=12\n", "12", "13", "FAIL", "12"},
		{"numeric_minor_not_lexical", "ID=debian\nVERSION_ID=12.10\n", "12.9", "12.11", "PASS", "12.10"},
		{"below_minimum", "ID=debian\nVERSION_ID=12.9\n", "12.10", "", "FAIL", "12.9"},
		{"above_maximum", "ID=debian\nVERSION_ID=12.10\n", "", "12.9", "FAIL", "12.10"},
		{"major_prefix_does_not_match", "ID=debian\nVERSION_ID=120\n", "12", "13", "FAIL", "120"},
		{"missing_segments_are_zero", "ID=debian\nVERSION_ID=12\n", "12.0.0", "12.0.0", "PASS", "12"},
		{"non_numeric_bounded_version", "ID=debian\nVERSION_ID=rolling\n", "12", "", "ERROR", ""},
		{"unbounded_rolling_version", "ID=debian\nVERSION_ID=Rolling~RC1^build+2\n", "", "", "PASS", "Rolling~RC1^build+2"},
		{"empty_source", "", "", "", "ERROR", ""},
		{"literal_single_quoted_metadata", "ID=debian\nVERSION_ID=12\nNAME='literal $HOME `id`'\n", "12", "12", "PASS", "12"},
		{"trailing_escape", "ID=debian\nVERSION_ID=12\\\n", "", "", "ERROR", ""},
		{"invalid_assignment_key", "1ID=debian\nVERSION_ID=12\n", "", "", "ERROR", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			content := tt.content
			if content != "" {
				content += "INTERNAL_NOTE='unrelated-private-field'\n"
			}
			fixtureFile(t, root, "etc/os-release", content)
			params := map[string]any{"allowed_ids": []any{"debian"}}
			if tt.minimum != "" {
				params["minimum_version"] = tt.minimum
			}
			if tt.maximum != "" {
				params["maximum_version"] = tt.maximum
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "O01", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("source parser must be exercised after successful cat")
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "/etc/os-release" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("os-release source or observation times lost")
				}
			}
			if tt.version != "" {
				if actual := evidenceProperty(t, results, "version_id"); actual != tt.version {
					t.Fatalf("version evidence want %q got %v", tt.version, actual)
				}
				_ = evidenceProperty(t, results, "os_id")
				_ = evidenceProperty(t, results, "raw_fields")
				if tt.name == "shell_escaped_unquoted_version" {
					raw := evidenceProperty(t, results, "raw_fields").(map[string]string)
					if raw["VERSION_ID"] != `12\.1` {
						t.Fatal("original os-release assignment was not preserved")
					}
				}
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "unrelated-private-field") {
				t.Fatal("unrelated os-release fields leaked into normalized evidence")
			}
		})
	}
}

func TestKernelCollectorPreservesReleaseAndRejectsMalformedOutput(t *testing.T) {
	for _, tt := range []struct {
		name, output, minimum, maximum, status, normalized, suffix string
		exit                                                       int
	}{
		{"distribution_suffix", "6.8.0-51-generic\n", "6.8", "6.8.0", "PASS", "6.8.0", "-51-generic", 0},
		{"rhel_suffix", "4.18.0-553.22.1.el8_10.x86_64\n", "4.18.0", "4.18.0", "PASS", "4.18.0", "-553.22.1.el8_10.x86_64", 0},
		{"plus_suffix", "6.1.0+\n", "6.1", "6.1.0", "PASS", "6.1.0", "+", 0},
		{"without_newline", "6.12.1-custom", "6.9", "", "PASS", "6.12.1", "-custom", 0},
		{"minor_numeric_boundary", "6.10.0\n", "6.9", "6.11", "PASS", "6.10.0", "", 0},
		{"below_minimum", "6.9.9\n", "6.10", "", "FAIL", "6.9.9", "", 0},
		{"above_maximum", "6.10.1\n", "6.0", "6.10.0", "FAIL", "6.10.1", "", 0},
		{"missing_patch", "6.1\n", "6.1.0", "6.1.0", "PASS", "6.1.0", "", 0},
		{"empty", "", "6.1", "", "ERROR", "", "", 0},
		{"multiple_lines", "6.1.0\n6.2.0\n", "6.1", "", "ERROR", "", "", 0},
		{"extra_blank_line", "6.1.0\n\n", "6.1", "", "ERROR", "", "", 0},
		{"leading_whitespace", " 6.1.0\n", "6.1", "", "ERROR", "", "", 0},
		{"suffix_whitespace", "6.1.0-custom build\n", "6.1", "", "ERROR", "", "", 0},
		{"invalid_numeric_segment", "6.x.0\n", "6.1", "", "ERROR", "", "", 0},
		{"missing_suffix_separator", "6.1.0garbage\n", "6.1", "", "ERROR", "", "", 0},
		{"nul_suffix", "6.1.0-custom\x00\n", "6.1", "", "ERROR", "", "", 0},
		{"overflow_segment", "18446744073709551616.1.0\n", "6.1", "", "ERROR", "", "", 0},
		{"query_failure", "6.8.0-test\n", "6.1", "", "ERROR", "6.8.0", "-test", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "kernel-output", tt.output)
			fixtureTool(t, root, "tools/uname", fmt.Sprintf(`[ "$*" = '-r' ] || exit 99; cat /kernel-output; exit %d`, tt.exit))
			params := map[string]any{"minimum_version": tt.minimum}
			if tt.maximum != "" {
				params["maximum_version"] = tt.maximum
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "O02", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != fmt.Sprint(tt.exit) {
				t.Fatal("uname exit status lost")
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "uname -r" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("kernel source or observation times lost")
				}
			}
			if raw := evidenceProperty(t, results, "kernel_raw"); raw != tt.output {
				t.Fatalf("raw kernel release changed: %q", raw)
			}
			if actual := evidenceProperty(t, results, "normalized_version"); actual != tt.normalized {
				t.Fatalf("normalized release want %q got %v", tt.normalized, actual)
			}
			if actual := evidenceProperty(t, results, "release_suffix"); actual != tt.suffix {
				t.Fatalf("release suffix want %q got %v", tt.suffix, actual)
			}
		})
	}
}

func TestOSReleaseCollectorReportsSourceAccessFailures(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_%v", missing), func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "etc/os-release", "ID=debian\nVERSION_ID=12\n")
			path := filepath.Join(root, "etc/os-release")
			if missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(path, 0000); err != nil {
				t.Fatal(err)
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "O01", map[string]any{"allowed_ids": []any{"debian"}}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("source access failure must be ERROR: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) == "0" {
				t.Fatal("cat access failure exit status lost")
			}
		})
	}
}
