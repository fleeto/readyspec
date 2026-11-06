//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func TestProcessCollectorRejectsMalformedTablesAndCountsBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, output, status string
		minimum, maximum     int
	}{
		{"zero_matching_allowed", "1 init\n2 unrelated\n", "PASS", 0, 0},
		{"zero_matching_required", "1 init\n2 unrelated\n", "FAIL", 1, 2},
		{"at_minimum", "1 init\n2 worker\n", "PASS", 1, 2},
		{"at_maximum", "1 init\n2 worker\n3 worker\n", "PASS", 1, 2},
		{"above_maximum", "1 init\n2 worker\n3 worker\n4 worker\n", "FAIL", 1, 2},
		{"exact_name_only", "1 init\n2 worker-extra\n3 other worker\n", "PASS", 0, 0},
		{"empty_output", "", "ERROR", 0, 0},
		{"garbage", "query failed without a process table\n", "ERROR", 0, 0},
		{"nul_output", "1 init\n2 work\x00er\n", "ERROR", 0, 0},
		{"invalid_pid", "pid worker\n", "ERROR", 0, 0},
		{"zero_pid", "0 worker\n", "ERROR", 0, 0},
		{"duplicate_pid", "1 init\n2 worker\n2 worker\n", "ERROR", 1, 2},
		{"damaged_unrelated_row", "1 init\n2 worker\nbroken\n", "ERROR", 1, 2},
		{"extra_blank_line", "1 init\n2 worker\n\n", "ERROR", 1, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "process-output", tt.output)
			fixtureTool(t, root, "tools/ps", `[ "$*" = '-eo pid=,comm=' ]; cat /process-output`)
			results := collectInSandbox(t, root, collectionArtifact(t, "R02", map[string]any{"name": "worker", "minimum_count": tt.minimum, "maximum_count": tt.maximum}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("malformed output must be rejected after successful ps execution")
			}
			if tt.status == "ERROR" && evidenceProperty(t, results, "error") != "INVALID_PROCESS_TABLE" {
				t.Fatal("invalid table classification missing")
			}
			if tt.status != "ERROR" {
				_ = evidenceProperty(t, results, "matched_count")
				_ = evidenceProperty(t, results, "process_records")
			}
		})
	}
}

func TestProcessCollectorCountsRealProcessesWithoutLeakingArguments(t *testing.T) {
	const helper = `import ctypes, json, subprocess, sys
children = []
worker = "import ctypes,sys,time; libc=ctypes.CDLL(None); result=libc.prctl(15,sys.argv[1].encode(),0,0,0); assert result==0; print('ready',flush=True); time.sleep(60)"
try:
    for name in ('rs-worker','rs-worker','rs-unrelated'):
        child = subprocess.Popen([sys.executable,'-c',worker,name,'--secret=process-private-sentinel'],stdout=subprocess.PIPE,text=True)
        children.append(child)
        assert child.stdout.readline() == 'ready\n'
    result = subprocess.run(['/goss','-g','/goss.json','validate','--format','json'],stdout=subprocess.PIPE)
    sys.stdout.buffer.write(result.stdout)
finally:
    for child in children:
        child.terminate()
    for child in children:
        child.wait()
`
	for _, tt := range []struct {
		name, target, status    string
		minimum, maximum, count int
	}{
		{"zero_allowed", "rs-absent", "PASS", 0, 0, 0},
		{"zero_required", "rs-absent", "FAIL", 1, 2, 0},
		{"exact_count", "rs-worker", "PASS", 2, 2, 2},
		{"above_maximum", "rs-worker", "FAIL", 0, 1, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			artifact := collectionArtifact(t, "R02", map[string]any{"name": tt.target, "minimum_count": tt.minimum, "maximum_count": tt.maximum})
			if err := os.WriteFile(filepath.Join(root, "goss.json"), artifact.Config, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := sandboxCommand(root, "/bin/python3", "-c", helper)
			for i, arg := range cmd.Args {
				if arg == "--" {
					args := append([]string{}, cmd.Args[:i]...)
					args = append(args, "--proc", "/proc")
					cmd.Args = append(args, cmd.Args[i:]...)
					break
				}
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("real process fixture failed: %v output=%s", err, out)
			}
			results, err := compiler.Normalize(artifact, out)
			if err != nil || len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v err=%v", tt.status, results, err)
			}
			if count := fmt.Sprint(evidenceProperty(t, results, "matched_count")); count != fmt.Sprint(tt.count) {
				t.Fatalf("want count %d got %s", tt.count, count)
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "ps -eo pid=,comm= (process names only)" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("process source or observation times missing")
				}
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "process-private-sentinel") || strings.Contains(string(raw), "rs-unrelated") {
				t.Fatal("process arguments or unrelated names leaked into evidence")
			}
		})
	}
}
