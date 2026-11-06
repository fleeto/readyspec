//go:build linux

package compiler_test

import (
	"fmt"
	"testing"
)

func TestMemTotalCollectorRejectsMalformedSourceAndPreservesBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, content, minimum, status string
		bytes                          string
		queryFailed                    bool
	}{
		{"duplicate_identical", "MemTotal: 1024 kB\nMemTotal: 1024 kB\n", "1 B", "ERROR", "", false},
		{"duplicate_conflicting", "MemTotal: 1024 kB\nMemTotal: 2048 kB\n", "1 B", "ERROR", "", false},
		{"duplicate_one_invalid", "MemTotal: 1024 kB\nMemTotal: corrupt kB\n", "1 B", "ERROR", "", false},
		{"negative", "MemTotal: -1 kB\n", "1 B", "ERROR", "", false},
		{"positive_sign", "MemTotal: +1024 kB\n", "1 B", "ERROR", "", false},
		{"negative_zero", "MemTotal: -0 kB\n", "1 B", "ERROR", "", false},
		{"zero_total", "MemTotal: 0 kB\n", "1 B", "ERROR", "", false},
		{"fraction", "MemTotal: 1.5 kB\n", "1 B", "ERROR", "", false},
		{"scientific_notation", "MemTotal: 1e6 kB\n", "1 B", "ERROR", "", false},
		{"invalid_unit", "MemTotal: 1024 MB\n", "1 B", "ERROR", "", false},
		{"missing_unit", "MemTotal: 1024\n", "1 B", "ERROR", "", false},
		{"extra_column", "MemTotal: 1024 kB extra\n", "1 B", "ERROR", "", false},
		{"missing_field", "MemAvailable: 1024 kB\n", "1 B", "ERROR", "", true},
		{"empty_source", "", "1 B", "ERROR", "", true},
		{"one_byte_below_threshold", "MemTotal: 1 kB\n", "1025 B", "FAIL", "1024", false},
		{"exact_threshold", "MemTotal: 1 kB\n", "1024 B", "PASS", "1024", false},
		{"one_byte_above_threshold", "MemTotal: 1 kB\n", "1023 B", "PASS", "1024", false},
		{"decimal_gb", "MemTotal: 1000000 kB\n", "1 GB", "PASS", "1024000000", false},
		{"binary_gib", "MemTotal: 1000000 kB\n", "1 GiB", "FAIL", "1024000000", false},
		{"large_exact", "MemTotal: 8796093022208 kB\n", "9007199254740992 B", "PASS", "9007199254740992", false},
		{"large_one_byte_below", "MemTotal: 8796093022208 kB\n", "9007199254740993 B", "FAIL", "9007199254740992", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/meminfo", tt.content)
			results := collectInSandbox(t, root, collectionArtifact(t, "H03", map[string]any{"min_memory": tt.minimum}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if !tt.queryFailed && fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("test must exercise data validation after successful source read")
			}
			if tt.queryFailed && fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "2" {
				t.Fatal("missing MemTotal must fail the original source collector")
			}
			if tt.bytes != "" {
				if actual := fmt.Sprint(evidenceProperty(t, results, "mem_total_bytes")); actual != tt.bytes {
					t.Fatalf("exact bytes want %s got %s", tt.bytes, actual)
				}
				_ = evidenceProperty(t, results, "mem_total_kib")
				_ = evidenceProperty(t, results, "raw")
			}
		})
	}
}

func TestSwapCollectorRejectsDamagedRecordsAndPreservesBoundaries(t *testing.T) {
	const header = "Filename Type Size Used Priority\n"
	for _, tt := range []struct{ name, record, mode, minimum, status, total string }{
		{"missing_column", "/swap file 1024 0\n", "minimum", "1 B", "ERROR", ""},
		{"extra_column", "/swap file 1024 0 -2 extra\n", "minimum", "1 B", "ERROR", ""},
		{"invalid_size", "/swap file bad 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"negative_size", "/swap file -1 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"positive_size_sign", "/swap file +1024 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"negative_zero_size", "/swap file -0 0 -2\n", "enabled", "", "ERROR", ""},
		{"fractional_size", "/swap file 1.5 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"scientific_size", "/swap file 1e3 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"invalid_used", "/swap file 1024 bad -2\n", "minimum", "1 B", "ERROR", ""},
		{"negative_used", "/swap file 1024 -1 -2\n", "minimum", "1 B", "ERROR", ""},
		{"positive_used_sign", "/swap file 1024 +1 -2\n", "minimum", "1 B", "ERROR", ""},
		{"used_exceeds_size", "/swap file 1024 1025 -2\n", "minimum", "1 B", "ERROR", ""},
		{"invalid_priority", "/swap file 1024 0 bad\n", "minimum", "1 B", "ERROR", ""},
		{"fractional_priority", "/swap file 1024 0 1.5\n", "minimum", "1 B", "ERROR", ""},
		{"positive_priority_sign", "/swap file 1024 0 +1\n", "minimum", "1 B", "ERROR", ""},
		{"invalid_type", "/swap unknown 1024 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"relative_device_path", "swap file 1024 0 -2\n", "minimum", "1 B", "ERROR", ""},
		{"duplicate_device", "/swap file 1024 0 -2\n/swap file 1024 0 -3\n", "minimum", "2 MiB", "ERROR", ""},
		{"bad_second_record", "/swap file 1024 0 -2\n/dev/swap partition broken 0 -3\n", "minimum", "1 B", "ERROR", ""},
		{"below_exact_sum", "/swap file 1 0 -2\n/dev/swap partition 2 2 10\n", "minimum", "3073 B", "FAIL", "3072"},
		{"exact_sum", "/swap file 1 0 -2\n/dev/swap partition 2 2 10\n", "minimum", "3072 B", "PASS", "3072"},
		{"above_exact_sum", "/swap file 1 0 -2\n/dev/swap partition 2 2 10\n", "minimum", "3071 B", "PASS", "3072"},
		{"large_exact", "/swap file 8796093022208 0 -2\n", "minimum", "9007199254740992 B", "PASS", "9007199254740992"},
		{"large_one_byte_below", "/swap file 8796093022208 0 -2\n", "minimum", "9007199254740993 B", "FAIL", "9007199254740992"},
		{"zero_size_active", "/swap file 0 0 -2\n", "enabled", "", "PASS", "0"},
		{"zero_size_not_disabled", "/swap file 0 0 -2\n", "disabled", "", "FAIL", "0"},
		{"escaped_file_and_partition", "/swap\\040file file 1 1 -3\n/dev/zram0 partition 2 0 100\n", "minimum", "3 KiB", "PASS", "3072"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/swaps", header+tt.record)
			params := map[string]any{"mode": tt.mode}
			if tt.minimum != "" {
				params["min_swap"] = tt.minimum
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "H04", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("test must exercise record parsing after successful read")
			}
			if tt.total != "" {
				if actual := fmt.Sprint(evidenceProperty(t, results, "total_bytes")); actual != tt.total {
					t.Fatalf("want %s bytes got %s", tt.total, actual)
				}
				_ = evidenceProperty(t, results, "devices")
				if evidenceProperty(t, results, "active") != true {
					t.Fatal("active device evidence lost")
				}
			}
		})
	}
}

func TestSwapCollectorRejectsInvalidSourceHeaders(t *testing.T) {
	for name, content := range map[string]string{
		"empty_source":    "",
		"missing_header":  "/swap file 1024 0 -2\n",
		"wrong_header":    "Filename Type Size Used\n/swap file 1024 0 -2\n",
		"repeated_header": "Filename Type Size Used Priority\nFilename Type Size Used Priority\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/swaps", content)
			results := collectInSandbox(t, root, collectionArtifact(t, "H04", map[string]any{"mode": "disabled"}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("damaged source cannot establish disabled Swap: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("header validation must be exercised after successful source read")
			}
		})
	}
}
