//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/template"
)

func TestPIDLimitsCollectorValidatesBoundsAndRecords(t *testing.T) {
	for _, tt := range []struct {
		name, limit, bound, content, status string
		minimum                             int
	}{
		{"nproc_soft_exact", "NPROC", "soft", "Max processes 32 64 processes\n", "PASS", 32},
		{"nproc_soft_below", "NPROC", "soft", "Max processes 32 64 processes\n", "FAIL", 33},
		{"nproc_hard_exact", "NPROC", "hard", "Max processes 32 64 processes\n", "PASS", 64},
		{"nproc_hard_below", "NPROC", "hard", "Max processes 32 64 processes\n", "FAIL", 65},
		{"nofile_unlimited_soft", "NOFILE", "soft", "Max open files unlimited unlimited files\n", "PASS", 1000000},
		{"nproc_unlimited_hard", "NPROC", "hard", "Max processes 32 unlimited processes\n", "PASS", 1000000},
		{"soft_zero", "NOFILE", "soft", "Max open files 0 64 files\n", "PASS", 0},
		{"large_exact", "NOFILE", "soft", "Max open files 9007199254740993 unlimited files\n", "PASS", 9007199254740993},
		{"large_below", "NOFILE", "soft", "Max open files 9007199254740992 unlimited files\n", "FAIL", 9007199254740993},
		{"duplicate_row", "NOFILE", "soft", "Max open files 32 64 files\nMax open files 0 1 files\n", "ERROR", 32},
		{"missing_units", "NOFILE", "soft", "Max open files 32 64\n", "ERROR", 32},
		{"wrong_units", "NOFILE", "soft", "Max open files 32 64 bytes\n", "ERROR", 32},
		{"extra_column", "NOFILE", "soft", "Max open files 32 64 files extra\n", "ERROR", 32},
		{"damaged_unselected_bound", "NOFILE", "soft", "Max open files 32 bogus files\n", "ERROR", 32},
		{"soft_above_hard", "NPROC", "hard", "Max processes 128 64 processes\n", "ERROR", 64},
		{"unlimited_soft_finite_hard", "NOFILE", "soft", "Max open files unlimited 64 files\n", "ERROR", 32},
		{"negative_value", "NOFILE", "soft", "Max open files -1 64 files\n", "ERROR", 0},
		{"positive_sign", "NOFILE", "soft", "Max open files +32 64 files\n", "ERROR", 32},
		{"fraction", "NOFILE", "soft", "Max open files 1/2 64 files\n", "ERROR", 0},
		{"missing_limit", "NPROC", "soft", "Max open files 32 64 files\n", "ERROR", 0},
		{"empty_source", "NOFILE", "soft", "", "ERROR", 0},
		{"nul_suffix", "NOFILE", "soft", "Max open files 32 64 files\x00\n", "ERROR", 32},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/42/limits", tt.content)
			results := collectInSandbox(t, root, collectionArtifact(t, "R04", map[string]any{"pid": 42, "limit": tt.limit, "bound": tt.bound, "minimum": tt.minimum}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("content must be validated after successful source read")
			}
			if tt.status != "ERROR" {
				if evidenceProperty(t, results, "limit") != tt.limit || evidenceProperty(t, results, "bound") != tt.bound {
					t.Fatal("selected limit evidence missing")
				}
				_ = evidenceProperty(t, results, "value")
			}
		})
	}
}

func TestUnitLimitsCollectorValidatesNPROCAndInfinity(t *testing.T) {
	for _, tt := range []struct{ name, bound, output, status string }{
		{"soft_exact", "soft", "64\n", "PASS"}, {"hard_below", "hard", "63\n", "FAIL"},
		{"soft_infinity", "soft", "infinity\n", "PASS"}, {"hard_unlimited", "hard", "unlimited\n", "PASS"},
		{"multiple_lines", "soft", "64\n64\n", "ERROR"}, {"extra_blank_line", "hard", "64\n\n", "ERROR"},
		{"fraction", "soft", "128/2\n", "ERROR"}, {"exponent", "hard", "6.4e1\n", "ERROR"}, {"empty", "soft", "", "ERROR"},
		{"query_failure", "soft", "", "ERROR"}, {"tool_missing", "hard", "", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "unit-limit", tt.output)
			property := "LimitNPROC"
			if tt.bound == "soft" {
				property += "Soft"
			}
			fixtureTool(t, root, "tools/systemctl", `[ "$*" = 'show --no-pager --value -p `+property+` -- app.service' ]; cat /unit-limit`)
			if tt.name == "query_failure" {
				fixtureTool(t, root, "tools/systemctl", "exit 2")
			}
			if tt.name == "tool_missing" {
				fixtureTool(t, root, "tools/systemctl", "exit 127")
			}
			p := catalogPlan(t, "R04")
			doc, _ := template.Lookup("R04", "1.0.0-config")
			raw, _ := json.Marshal(doc)
			var snapshot plan.CheckTemplate
			_ = json.Unmarshal(raw, &snapshot)
			p.EmbeddedTemplates = []plan.CheckTemplate{snapshot}
			item := &p.Roles[0].Items[0]
			item.TemplateVersion = snapshot.Version
			item.TemplateDigest = snapshot.Digest
			item.Params = map[string]any{"unit": "app.service", "limit": "NPROC", "bound": tt.bound, "minimum": 64}
			results := collectInSandbox(t, root, selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}}))
			if len(results) != 1 || results[0].Status != tt.status || results[0].EvidenceMode != "CONFIG" {
				t.Fatalf("want %s CONFIG got %+v", tt.status, results)
			}
		})
	}
}

func TestPIDLimitsCollectorRejectsUnreadableSource(t *testing.T) {
	for _, mode := range []string{"missing", "unreadable", "parent_denied"} {
		t.Run(mode, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/42/limits", "Max processes 32 64 processes\n")
			path := filepath.Join(root, "proc/42/limits")
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
			results := collectInSandbox(t, root, collectionArtifact(t, "R04", map[string]any{"pid": 42, "limit": "NPROC", "minimum": 0}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("source failure cannot pass even at zero minimum: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) == "0" {
				t.Fatal("source access error exit code lost")
			}
		})
	}
}

func TestPIDLimitsCollectorObservesRealChildAndDisappearance(t *testing.T) {
	const helper = `import json, subprocess, sys
worker = "import resource,time; resource.setrlimit(resource.RLIMIT_NOFILE,(32,64)); resource.setrlimit(resource.RLIMIT_NPROC,(32,64)); print('ready',flush=True); time.sleep(60)"
child = subprocess.Popen([sys.executable,'-c',worker],stdout=subprocess.PIPE,text=True)
try:
    assert child.pid == 2, ('unexpected isolated PID',child.pid)
    assert child.stdout.readline() == 'ready\n'
    if sys.argv[1] == 'gone':
        child.terminate()
        child.wait()
    result = subprocess.run(['/goss','-g','/goss.json','validate','--format','json'],stdout=subprocess.PIPE)
    sys.stdout.buffer.write(result.stdout)
finally:
    if child.poll() is None:
        child.terminate()
    child.wait()
`
	for _, tt := range []struct {
		name, limit, bound, status string
		minimum                    int
	}{
		{"real_nproc_soft", "NPROC", "soft", "PASS", 32}, {"real_nproc_hard", "NPROC", "hard", "PASS", 64},
		{"real_nofile_over_soft", "NOFILE", "soft", "FAIL", 33}, {"real_nofile_hard", "NOFILE", "hard", "PASS", 64},
		{"gone", "NOFILE", "soft", "ERROR", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			a := collectionArtifact(t, "R04", map[string]any{"pid": 2, "limit": tt.limit, "bound": tt.bound, "minimum": tt.minimum})
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := sandboxCommand(root, "/bin/python3", "-c", helper, tt.name)
			for i, arg := range cmd.Args {
				if arg == "--" {
					args := append([]string{}, cmd.Args[:i]...)
					args = append(args, "--proc", "/proc", "--as-pid-1")
					cmd.Args = append(args, cmd.Args[i:]...)
					break
				}
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("real limit fixture failed: %v output=%s", err, out)
			}
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v err=%v", tt.status, results, err)
			}
			if tt.name != "gone" {
				if evidenceProperty(t, results, "source_kind") != "effective_process_limit" || results[0].EvidenceMode != "EFFECTIVE_STATE" {
					t.Fatal("effective limit evidence lost")
				}
				if evidenceProperty(t, results, "soft") != "32" || evidenceProperty(t, results, "hard") != "64" {
					t.Fatal("real soft and hard limits were not preserved")
				}
				for _, entry := range results[0].Evidence {
					if entry.Source != "/proc/2/limits" || entry.StartTime == "" || entry.EndTime == "" {
						t.Fatal("PID source or observation times lost")
					}
				}
			} else if fmt.Sprint(evidenceProperty(t, results, "exit-status")) == "0" {
				t.Fatal("disappeared PID read failure lost")
			}
		})
	}
}
