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

func TestLoadCollectorRejectsMalformedSource(t *testing.T) {
	for name, content := range map[string]string{
		"empty":                      "",
		"missing_fields":             "1.00\n",
		"missing_queue_and_pid":      "1.00 2.00 3.00\n",
		"missing_pid":                "1.00 2.00 3.00 1/100\n",
		"extra_field":                "1.00 2.00 3.00 1/100 42 extra\n",
		"bad_unselected_window":      "1.00 broken 3.00 1/100 42\n",
		"negative_unselected_window": "1.00 2.00 -3.00 1/100 42\n",
		"negative":                   "-1.00 2.00 3.00 1/100 42\n",
		"positive_sign":              "+1.00 2.00 3.00 1/100 42\n",
		"fraction":                   "1/2 2.00 3.00 1/100 42\n",
		"exponent":                   "1e0 2.00 3.00 1/100 42\n",
		"nan":                        "NaN 2.00 3.00 1/100 42\n",
		"infinity":                   "Inf 2.00 3.00 1/100 42\n",
		"hex":                        "0x1 2.00 3.00 1/100 42\n",
		"invalid_queue":              "1.00 2.00 3.00 broken 42\n",
		"negative_queue":             "1.00 2.00 3.00 -1/100 42\n",
		"fractional_queue":           "1.00 2.00 3.00 1.5/100 42\n",
		"zero_task_total":            "1.00 2.00 3.00 0/0 42\n",
		"invalid_pid":                "1.00 2.00 3.00 1/100 nope\n",
		"negative_pid":               "1.00 2.00 3.00 1/100 -1\n",
		"nul":                        "1.00 2.00 3.00 1/100 42\x00\n",
		"extra_blank_line":           "1.00 2.00 3.00 1/100 42\n\n",
		"multiple_records":           "1.00 2.00 3.00 1/100 42\n1.00 2.00 3.00 1/100 43\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/loadavg", content)
			results := collectInSandbox(t, root, collectionArtifact(t, "R03", map[string]any{"window": 1, "maximum": 100}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("damaged loadavg must be ERROR: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("malformed content must reach validation after a successful read")
			}
			if evidenceProperty(t, results, "error") != "INVALID_LOADAVG" {
				t.Fatal("invalid source classification missing")
			}
		})
	}
}

func TestLoadCollectorPreservesWindowThresholdBoundaries(t *testing.T) {
	for index, window := range []int{1, 5, 15} {
		for _, tt := range []struct{ name, value, maximum, status string }{
			{"below", "2.49", "2.50", "PASS"},
			{"exact", "2.50", "2.50", "PASS"},
			{"above", "2.51", "2.50", "FAIL"},
			{"zero", "0.00", "0", "PASS"},
			{"large_fraction_above", "9007199254740992.01", "9007199254740992", "FAIL"},
		} {
			t.Run(fmt.Sprintf("window_%d/%s", window, tt.name), func(t *testing.T) {
				root := collectionRoot(t)
				loads := []string{"0.11", "0.22", "0.33"}
				loads[index] = tt.value
				fixtureFile(t, root, "proc/loadavg", strings.Join(loads, " ")+" 1/100 42\n")
				results := collectInSandbox(t, root, collectionArtifact(t, "R03", map[string]any{"window": window, "maximum": json.Number(tt.maximum)}))
				if len(results) != 1 || results[0].Status != tt.status {
					t.Fatalf("want %s got %+v", tt.status, results)
				}
				if actual := evidenceProperty(t, results, "load_average"); actual != tt.value {
					t.Fatalf("selected load want %s got %v", tt.value, actual)
				}
				if actual := fmt.Sprint(evidenceProperty(t, results, "window_minutes")); actual != fmt.Sprint(window) {
					t.Fatalf("window want %d got %s", window, actual)
				}
				for _, entry := range results[0].Evidence {
					if entry.Source != "/proc/loadavg" || entry.StartTime == "" || entry.EndTime == "" {
						t.Fatal("load source or observation time missing")
					}
				}
			})
		}
	}
}

func TestLoadCollectorAcceptsValidSourceLayouts(t *testing.T) {
	for name, content := range map[string]string{
		"without_final_newline": "1.00 2.00 3.00 1/100 42",
		"tabs_and_padding":      " 1.00\t2.00\t3.00\t1/100\t42 \n",
		"integer_loads":         "1 2 3 1/100 42\n",
		"zero_running_tasks":    "0.00 0.00 0.00 0/100 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/loadavg", content)
			results := collectInSandbox(t, root, collectionArtifact(t, "R03", map[string]any{"window": 15, "maximum": 3}))
			if len(results) != 1 || results[0].Status != "PASS" {
				t.Fatalf("valid layout rejected: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "window_minutes")) != "15" {
				t.Fatal("window evidence missing")
			}
		})
	}
}

func TestLoadCollectorReportsSourceAccessFailures(t *testing.T) {
	for _, mode := range []string{"missing", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/loadavg", "1.00 2.00 3.00 1/100 42\n")
			path := filepath.Join(root, "proc/loadavg")
			if mode == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(path, 0000); err != nil {
				t.Fatal(err)
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "R03", map[string]any{"window": 1, "maximum": 100}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("source failure must be ERROR: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) == "0" {
				t.Fatal("source access failure exit status lost")
			}
		})
	}
}
