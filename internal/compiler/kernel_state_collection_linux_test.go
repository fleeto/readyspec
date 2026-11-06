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

func TestSysctlCollectorValidatesRangesAndObservations(t *testing.T) {
	for _, tt := range []struct {
		name, content, status string
		params                map[string]any
	}{
		{"at_minimum", "10\n", "PASS", map[string]any{"minimum": 10, "maximum": 20}},
		{"at_maximum", "20\n", "PASS", map[string]any{"minimum": 10, "maximum": 20}},
		{"below_minimum", "9\n", "FAIL", map[string]any{"minimum": 10, "maximum": 20}},
		{"above_maximum", "21\n", "FAIL", map[string]any{"minimum": 10, "maximum": 20}},
		{"minimum_only", "10\n", "PASS", map[string]any{"minimum": 10}},
		{"maximum_only", "20\n", "PASS", map[string]any{"maximum": 20}},
		{"negative_boundary", "-10\n", "PASS", map[string]any{"minimum": -20, "maximum": -10}},
		{"negative_above_maximum", "-9\n", "FAIL", map[string]any{"minimum": -20, "maximum": -10}},
		{"decimal_boundary", "2.5\n", "PASS", map[string]any{"minimum": 1.5, "maximum": 2.5}},
		{"large_exact", "9007199254740993\n", "PASS", map[string]any{"minimum": json.Number("9007199254740993")}},
		{"large_below", "9007199254740992\n", "FAIL", map[string]any{"minimum": json.Number("9007199254740993")}},
		{"empty_numeric", "", "ERROR", map[string]any{"minimum": 0}},
		{"nan", "NaN\n", "ERROR", map[string]any{"minimum": 0}},
		{"infinity", "Inf\n", "ERROR", map[string]any{"minimum": 0}},
		{"garbage", "invalid\n", "ERROR", map[string]any{"minimum": 0}},
		{"rational_is_not_scalar_decimal", "1/2\n", "ERROR", map[string]any{"minimum": 0, "maximum": 1}},
		{"hex_is_not_scalar_decimal", "0x10\n", "ERROR", map[string]any{"minimum": 0, "maximum": 20}},
		{"scientific_notation", "1e1\n", "ERROR", map[string]any{"minimum": 0, "maximum": 20}},
		{"vertical_tab", "\v10\n", "ERROR", map[string]any{"minimum": 0, "maximum": 20}},
		{"unicode_space", "\u00a010\n", "ERROR", map[string]any{"minimum": 0, "maximum": 20}},
		{"multiple_values", "10\t20\n", "ERROR", map[string]any{"minimum": 0}},
		{"multiple_lines", "10\n20\n", "ERROR", map[string]any{"minimum": 0}},
		{"extra_blank_line", "10\n\n", "ERROR", map[string]any{"minimum": 0, "maximum": 20}},
		{"nul_is_not_discarded", "1\x000\n", "ERROR", map[string]any{"minimum": 0, "maximum": 20}},
		{"exact_numeric", "10\n", "PASS", map[string]any{"value": 10}},
		{"exact_text_vector", "10\t20\n", "PASS", map[string]any{"value": "10\t20"}},
		{"exact_text_rational", "1/2\n", "PASS", map[string]any{"value": "1/2"}},
		{"empty_text", "\n", "PASS", map[string]any{"value": ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/sys/vm/swappiness", tt.content)
			params := map[string]any{"name": "vm.swappiness"}
			for key, value := range tt.params {
				params[key] = value
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "O03", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("successful read must reach observation validation")
			}
			if raw := evidenceProperty(t, results, "value_raw"); raw != tt.content {
				t.Fatalf("raw sysctl changed: want %q got %q", tt.content, raw)
			}
		})
	}
}

func TestModuleCollectorValidatesWholeTableAndNormalizesNames(t *testing.T) {
	const target = "ip_tables 32768 0 - Live 0x0000000000000000\n"
	for _, tt := range []struct {
		name, content, module, status string
		loaded                        bool
	}{
		{"empty_absent", "", "ip_tables", "PASS", false},
		{"empty_required", "", "ip_tables", "FAIL", true},
		{"one_loaded", target, "ip_tables", "PASS", true},
		{"hyphen_normalization", target, "ip-tables", "PASS", true},
		{"unexpected_loaded", target, "ip_tables", "FAIL", false},
		{"multiple_modules", "overlay 8192 0 - Live 0x0\n" + target + "nf_tables 65536 1 ip_tables, Live 0x0\n", "ip-tables", "PASS", true},
		{"unload_disabled", "ip_tables 32768 - - Live 0x0\n", "ip_tables", "PASS", true},
		{"tainted_module", "ip_tables 32768 0 - Live 0x0 (OE)\n", "ip_tables", "PASS", true},
		{"permanent_module", "ip_tables 32768 0 [permanent], Live 0x0\n", "ip_tables", "PASS", true},
		{"short_record", "ip_tables\n", "ip_tables", "ERROR", true},
		{"unrelated_damage", "broken_record\n", "ip_tables", "ERROR", false},
		{"damage_after_target", target + "broken_record\n", "ip_tables", "ERROR", true},
		{"duplicate_module", target + target, "ip_tables", "ERROR", true},
		{"invalid_size", "ip_tables NaN 0 - Live 0x0\n", "ip_tables", "ERROR", true},
		{"negative_size", "ip_tables -1 0 - Live 0x0\n", "ip_tables", "ERROR", true},
		{"invalid_refcount", "ip_tables 32768 bogus - Live 0x0\n", "ip_tables", "ERROR", true},
		{"invalid_dependency", "ip_tables 32768 1 invalid/name, Live 0x0\n", "ip_tables", "ERROR", true},
		{"invalid_state", "ip_tables 32768 0 - Unknown 0x0\n", "ip_tables", "ERROR", true},
		{"invalid_address", "ip_tables 32768 0 - Live not-an-address\n", "ip_tables", "ERROR", true},
		{"invalid_extra_column", "ip_tables 32768 0 - Live 0x0 unexpected\n", "ip_tables", "ERROR", true},
		{"loading_target", "ip_tables 32768 0 - Loading 0x0\n", "ip_tables", "ERROR", true},
		{"unloading_target", "ip_tables 32768 0 - Unloading 0x0\n", "ip_tables", "ERROR", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/modules", tt.content)
			results := collectInSandbox(t, root, collectionArtifact(t, "O04", map[string]any{"name": tt.module, "loaded": tt.loaded}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("module parser must run after a successful source read")
			}
			if tt.status != "ERROR" {
				if got := evidenceProperty(t, results, "name"); got != "ip_tables" {
					t.Fatalf("normalized name missing: %v", got)
				}
				_ = evidenceProperty(t, results, "loaded")
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "0x") {
				t.Fatal("unneeded kernel addresses leaked into evidence")
			}
		})
	}
}

func TestKernelStateCollectorsClassifySourceFailures(t *testing.T) {
	for _, id := range []string{"O03", "O04"} {
		for _, mode := range []string{"missing", "unreadable", "parent_denied"} {
			t.Run(id+"/"+mode, func(t *testing.T) {
				root := collectionRoot(t)
				source := "proc/modules"
				params := map[string]any{"name": "ip_tables", "loaded": false}
				if id == "O03" {
					source = "proc/sys/vm/swappiness"
					params = map[string]any{"name": "vm.swappiness", "minimum": 0}
				}
				fixtureFile(t, root, source, "10\n")
				path := filepath.Join(root, source)
				switch mode {
				case "missing":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "unreadable":
					if err := os.Chmod(path, 0000); err != nil {
						t.Fatal(err)
					}
				case "parent_denied":
					parent := filepath.Dir(path)
					if err := os.Chmod(parent, 0000); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
				}
				results := collectInSandbox(t, root, collectionArtifact(t, id, params))
				want := "ERROR"
				if id == "O03" && mode == "missing" {
					want = "FAIL"
				}
				if len(results) != 1 || results[0].Status != want {
					t.Fatalf("want %s got %+v", want, results)
				}
			})
		}
	}
}
