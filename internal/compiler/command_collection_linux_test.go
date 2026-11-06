//go:build linux

package compiler_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func TestCommandCollectorChecksPathsWithoutExecutingTargets(t *testing.T) {
	for _, tt := range []struct {
		name, command, kind        string
		mode                       os.FileMode
		executable                 bool
		status, wantPath, wantType string
	}{
		{"absolute_executable", "/opt/probe", "file", 0755, true, "PASS", "/opt/probe", "file"},
		{"absolute_not_executable", "/opt/probe", "file", 0644, true, "FAIL", "/opt/probe", "file"},
		{"absolute_no_exec_requirement", "/opt/probe", "file", 0644, false, "PASS", "/opt/probe", "file"},
		{"metadata_without_read_permission", "/opt/probe", "file", 0000, false, "PASS", "/opt/probe", "file"},
		{"relative_with_slash", "opt/probe", "file", 0755, true, "PASS", "/opt/probe", "file"},
		{"path_executable", "probe", "file", 0755, true, "PASS", "/tools/probe", "file"},
		{"path_not_executable", "probe", "file", 0644, true, "FAIL", "/tools/probe", "file"},
		{"path_empty_component", "probe", "file", 0755, true, "PASS", "/probe", "file"},
		{"path_relative_component", "probe", "file", 0755, true, "PASS", "/tools/probe", "file"},
		{"path_falls_through_to_executable", "cat", "file", 0644, true, "PASS", "/bin/cat", "file"},
		{"directory_is_not_command", "/opt/probe", "dir", 0755, false, "FAIL", "/opt/probe", "directory"},
		{"missing_absolute", "/opt/missing", "missing", 0, true, "FAIL", "/opt/missing", ""},
		{"missing_from_path", "absent-command", "missing", 0, true, "FAIL", "", ""},
		{"accessible_symlink", "/opt/probe", "symlink", 0777, true, "PASS", "/opt/probe", "file"},
		{"dangling_symlink", "/opt/probe", "dangling", 0777, true, "FAIL", "/opt/probe", ""},
		{"symlink_loop", "/opt/probe", "loop", 0777, true, "ERROR", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			sentinel := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "sentinel"), 0755); err != nil {
				t.Fatal(err)
			}
			if tt.kind == "dir" {
				if err := os.MkdirAll(filepath.Join(root, "opt/probe"), 0755); err != nil {
					t.Fatal(err)
				}
			} else if tt.kind != "missing" {
				target := "opt/probe"
				if tt.command == "probe" {
					target = "tools/probe"
				}
				if tt.command == "cat" {
					target = "tools/cat"
				}
				if tt.name == "path_empty_component" {
					target = "probe"
				}
				fixtureFile(t, root, target, "#!/bin/sh\nprintf executed > /sentinel/target-ran\nexit 0\n")
				if err := os.Chmod(filepath.Join(root, target), tt.mode); err != nil {
					t.Fatal(err)
				}
				if tt.kind == "symlink" || tt.kind == "dangling" || tt.kind == "loop" {
					if err := os.Rename(filepath.Join(root, "opt/probe"), filepath.Join(root, "opt/target")); err != nil {
						t.Fatal(err)
					}
					target := "target"
					if tt.kind == "dangling" {
						target = "missing"
					}
					if tt.kind == "loop" {
						target = "probe"
					}
					if err := os.Symlink(target, filepath.Join(root, "opt/probe")); err != nil {
						t.Fatal(err)
					}
				}
			}
			params := map[string]any{"command": tt.command, "executable": tt.executable}
			artifact := collectionArtifact(t, "D02", params)
			var diagnostics bytes.Buffer
			// The positive control invokes the original executable once before checking
			// the artifact, proving that its execution sentinel is live.
			if tt.name == "absolute_executable" {
				cmd := sandboxCommand(root, "/bin/sh", "-c", "/opt/probe")
				cmd.Stderr = &diagnostics
				for i, arg := range cmd.Args {
					if arg == "--" {
						args := append([]string{}, cmd.Args[:i]...)
						args = append(args, "--bind", sentinel, "/sentinel")
						cmd.Args = append(args, cmd.Args[i:]...)
						break
					}
				}
				if _, err := cmd.Output(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(sentinel, "target-ran")); err != nil {
					t.Fatalf("execution sentinel did not fire: %v", err)
				}
				if err := os.Remove(filepath.Join(sentinel, "target-ran")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "goss.json"), artifact.Config, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := sandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json")
			for i, arg := range cmd.Args {
				if arg == "--" {
					args := append([]string{}, cmd.Args[:i]...)
					args = append(args, "--bind", sentinel, "/sentinel")
					if tt.name == "path_empty_component" {
						args = append(args, "--setenv", "PATH", ":/bin")
					}
					if tt.name == "path_relative_component" {
						args = append(args, "--setenv", "PATH", "tools:/bin")
					}
					cmd.Args = append(args, cmd.Args[i:]...)
					break
				}
			}
			cmd.Stderr = &diagnostics
			out, err := cmd.Output()
			if err != nil && len(out) == 0 {
				t.Fatalf("D02 original artifact failed: %v: %s", err, diagnostics.String())
			}
			results, err := compiler.Normalize(artifact, out)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(sentinel, "target-ran")); !os.IsNotExist(err) {
				t.Fatalf("artifact executed target probe: %v", err)
			}
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if tt.status == "ERROR" {
				if evidenceProperty(t, results, "error") != "PATH_LOOKUP_FAILED" {
					t.Fatal("lookup error classification lost")
				}
				return
			}
			if tt.wantPath != "" {
				if path := evidenceProperty(t, results, "path"); path != tt.wantPath {
					t.Fatalf("path want %q got %v", tt.wantPath, path)
				}
				if tt.wantType != "" {
					if kind := evidenceProperty(t, results, "filetype"); kind != tt.wantType {
						t.Fatalf("type want %q got %v", tt.wantType, kind)
					}
				}
			} else if evidenceProperty(t, results, "exists") != false {
				t.Fatal("missing command evidence lost")
			}
			if evidenceProperty(t, results, "execution_required") != tt.executable {
				t.Fatal("requested executable policy not preserved")
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "exit 0") || strings.Contains(string(raw), "target-ran") {
				t.Fatal("target script content leaked into evidence")
			}
		})
	}
}

func TestCommandCollectorClassifiesUnsearchablePaths(t *testing.T) {
	for _, tt := range []struct {
		name, command string
		expect        string
	}{
		{"absolute_parent_denied", "/blocked/probe", "ERROR"},
		{"path_entry_denied", "probe", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "blocked/probe", "#!/bin/sh\nexit 0\n")
			if err := os.Chmod(filepath.Join(root, "blocked"), 0000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "blocked"), 0755) })
			if tt.command == "probe" {
				if err := os.Symlink("/blocked", filepath.Join(root, "tools/blocked-dir")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/blocked/probe", filepath.Join(root, "tools/probe")); err != nil {
					t.Fatal(err)
				}
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "D02", map[string]any{"command": tt.command, "executable": false}))
			if len(results) != 1 || results[0].Status != tt.expect {
				t.Fatalf("want %s got %+v", tt.expect, results)
			}
			if evidenceProperty(t, results, "error") != "PATH_LOOKUP_FAILED" || evidenceProperty(t, results, "errno") != "EACCES" {
				t.Fatal("access failure reason lost")
			}
		})
	}
}
