//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUptimeCollectorValidatesCompleteRecord(t *testing.T) {
	for _, tt := range []struct {
		name, content, status string
		minimum, maximum      float64
	}{
		{"selected_window_below", "0.50 1234.56\n", "FAIL", 1, 0},
		{"selected_window_exact", "1.00 0.00\n", "PASS", 1, 0},
		{"selected_window_above", "1.51 0.00\n", "FAIL", 2, 0},
		{"maximum_exact", "120.00 3.37\n", "PASS", 60, 120},
		{"maximum_above", "120.01 3.37\n", "FAIL", 60, 120},
		{"missing_idle_field", "1.00\n", "ERROR", 1, 0},
		{"extra_third_field", "9.99 6.00 ignore\n", "ERROR", 1, 0},
		{"idle_not_numeric", "1.00 6x\n", "ERROR", 1, 0},
		{"extra_line", "1.00 2.00\n3.00 4.00\n", "ERROR", 1, 0},
		{"nul_inside_record", "1.00 2.00\x00\n", "ERROR", 1, 0},
		{"empty_source", "", "ERROR", 1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/uptime", tt.content)
			params := map[string]any{"minimum_seconds": tt.minimum}
			if tt.maximum > 0 {
				params["maximum_seconds"] = tt.maximum
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "L01", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if tt.status != "ERROR" {
				if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
					t.Fatal("record must be validated after a successful read")
				}
				if evidenceProperty(t, results, "uptime_seconds") == nil {
					t.Fatal("observed uptime evidence missing")
				}
			}
		})
	}
}

func TestUptimeCollectorComparesExactDecimalBounds(t *testing.T) {
	for _, tt := range []struct{ name, content, minimum, maximum, status string }{
		{"exact_at_limit", "9007199254740993.00 0.00\n", "9007199254740993", "", "PASS"},
		{"just_below_limit", "9007199254740992.50 0.00\n", "9007199254740993", "", "FAIL"},
		{"just_above_maximum", "9007199254740993.01 0.00\n", "9007199254740993", "9007199254740993", "FAIL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/uptime", tt.content)
			params := map[string]any{"minimum_seconds": json.Number(tt.minimum)}
			if tt.maximum != "" {
				params["maximum_seconds"] = json.Number(tt.maximum)
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "L01", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}

func TestUptimeCollectorReportsSourceAccessFailures(t *testing.T) {
	for _, mode := range []string{"missing", "parent_denied"} {
		t.Run(mode, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/uptime", "1.00 2.00\n")
			path := filepath.Join(root, "proc/uptime")
			if mode == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				parent := filepath.Dir(path)
				if err := os.Chmod(parent, 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "L01", map[string]any{"minimum_seconds": 0}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("source failure without minimum seconds must ERROR: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) == "0" {
				t.Fatal("source access failure exit code lost")
			}
		})
	}
}
