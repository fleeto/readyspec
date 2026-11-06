//go:build linux

package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOnlineCPUCollectorUsesRealSysfsAndGetconf(t *testing.T) {
	for _, layout := range []struct {
		name, online string
		count        int
	}{
		{"two_online", "0-1", 2},
		{"nonzero_online_indices", "4-5", 2},
		{"sparse_online_indices", "0,2,4,6", 4},
	} {
		t.Run(layout.name, func(t *testing.T) {
			root := collectionRoot(t)
			for index := 0; index < 8; index++ {
				if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("sys/devices/system/cpu/cpu%d", index)), 0755); err != nil {
					t.Fatal(err)
				}
			}
			fixtureFile(t, root, "sys/devices/system/cpu/online", layout.online+"\n")
			fixtureFile(t, root, "sys/devices/system/cpu/present", "0-7\n")
			fixtureFile(t, root, "sys/devices/system/cpu/possible", "0-7\n")
			// No getconf shim is installed. Both this independent configured-count
			// probe and the original artifact use the actual tool and libc in the
			// same namespace; only sysfs consists of test data.
			if _, err := os.Lstat(filepath.Join(root, "tools/getconf")); !os.IsNotExist(err) {
				t.Fatal("real getconf must not be replaced")
			}
			output, err := sandboxCommand(root, "/bin/getconf", "_NPROCESSORS_CONF").Output()
			if err != nil {
				t.Fatalf("configured CPU query: %v", err)
			}
			configured, err := strconv.Atoi(strings.TrimSpace(string(output)))
			if err != nil || configured != 8 {
				t.Fatalf("fixture must have 8 configured CPUs: %q %v", output, err)
			}
			for _, minimum := range []int{layout.count, layout.count + 1, configured} {
				t.Run(fmt.Sprintf("minimum_%d", minimum), func(t *testing.T) {
					artifact := collectionArtifact(t, "H02", map[string]any{"min_cores": minimum})
					before := time.Now()
					results := collectInSandbox(t, root, artifact)
					after := time.Now()
					want := "FAIL"
					if minimum <= layout.count {
						want = "PASS"
					}
					if len(results) != 1 || results[0].Status != want {
						t.Fatalf("online=%s configured=%d minimum=%d: want %s got %+v", layout.online, configured, minimum, want, results)
					}
					count, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(evidenceProperty(t, results, "stdout"))))
					if err != nil || count != layout.count {
						t.Fatalf("must observe online count %d, got %d (%v)", layout.count, count, err)
					}
					for _, entry := range results[0].Evidence {
						if entry.Source != "getconf _NPROCESSORS_ONLN (online logical processors)" {
							t.Fatalf("wrong observation source: %q", entry.Source)
						}
						start, e1 := time.Parse(time.RFC3339Nano, entry.StartTime)
						end, e2 := time.Parse(time.RFC3339Nano, entry.EndTime)
						if e1 != nil || e2 != nil || end.Before(start) || start.Before(before) || end.After(after) || entry.DurationNS < 0 {
							t.Fatalf("invalid collection interval: %+v", entry)
						}
					}
				})
			}
		})
	}
}
