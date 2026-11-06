//go:build linux

package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func TestRuntimeCollectorValidatesVersionQueries(t *testing.T) {
	for _, tt := range []struct {
		name, runtime, version, minimum, maximum, status, normalized string
		exit                                                         int
	}{
		{"python_path", "python", "Python 3.12.1\n", "3.12.1", "3.12.1", "PASS", "3.12.1", 0},
		{"node_path", "node", "v20.10.0\n", "20.9", "20.11", "PASS", "20.10.0", 0},
		{"java_legacy_path", "java", "openjdk version \"1.8.0_422\"\nOpenJDK Runtime Environment (build fixture)\n", "8", "8.0.422", "PASS", "8.0.422", 0},
		{"java_modern_path", "java", "openjdk 21.0.2 2024-01-16\nOpenJDK Runtime Environment (build fixture)\n", "21.0.2", "21.0.2", "PASS", "21.0.2", 0},
		{"python_below", "python", "Python 3.10.0\n", "3.11", "", "FAIL", "3.10.0", 0},
		{"node_above", "node", "v20.10.1\n", "20", "20.10.0", "FAIL", "20.10.1", 0},
		{"python_prerelease", "python", "Python 3.13.0rc1\n", "3.13", "3.13.0", "PASS", "3.13.0", 0},
		{"node_build_suffix", "node", "v20.10.0-custom\n", "20", "20.10", "PASS", "20.10.0", 0},
		{"java_ea_suffix", "java", "openjdk version \"21-ea\"\n", "21", "21", "PASS", "21", 0},
		{"python_garbage_suffix", "python", "Python 3.12.1garbage\n", "3", "", "ERROR", "", 0},
		{"node_garbage_suffix", "node", "v20.10.0garbage\n", "20", "", "ERROR", "", 0},
		{"java_unclosed_quote", "java", "openjdk version \"17.0.1\n", "17", "", "ERROR", "", 0},
		{"banner_before_java", "java", "unrecognized banner\njava version \"17.0.1\"\n", "17", "", "ERROR", "", 0},
		{"duplicate_python_version", "python", "Python 3.12.1\nPython 3.10.0\n", "3.12", "", "ERROR", "", 0},
		{"duplicate_java_version", "java", "java version \"17.0.1\"\njava version \"21.0.1\"\n", "17", "", "ERROR", "", 0},
		{"empty_version", "python", "", "3", "", "ERROR", "", 0},
		{"nonstandard", "node", "not a node version\n", "20", "", "ERROR", "", 0},
		{"nul_version", "python", "Python 3.12.1\x00\n", "3", "", "ERROR", "", 0},
		{"extra_blank_line", "python", "Python 3.12.1\n\n", "3", "", "ERROR", "", 0},
		{"query_failure", "python", "Python 3.12.1\n", "3", "", "ERROR", "", 2},
		{"query_exit_127_is_not_missing", "node", "v20.10.0\n", "20", "", "ERROR", "", 127},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "runtime-version", tt.version)
			flag := "--version"
			if tt.runtime == "java" {
				flag = "-version"
			}
			fixtureTool(t, root, "opt/runtime", fmt.Sprintf(`[ "$#" = 1 ] && [ "$1" = '%s' ] || exit 99; cat /runtime-version; exit %d`, flag, tt.exit))
			params := map[string]any{"runtime": tt.runtime, "path": "/opt/runtime", "minimum_version": tt.minimum}
			if tt.maximum != "" {
				params["maximum_version"] = tt.maximum
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "D03", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if raw := evidenceProperty(t, results, "version_raw"); raw != tt.version {
				t.Fatalf("version original lost: want %q got %q", tt.version, raw)
			}
			if tt.exit != 0 && evidenceProperty(t, results, "error") != "RUNTIME_QUERY_FAILED" {
				t.Fatal("failed version query was not classified explicitly")
			}
			if tt.normalized != "" {
				if actual := evidenceProperty(t, results, "normalized_version"); actual != tt.normalized {
					t.Fatalf("normalized want %s got %v", tt.normalized, actual)
				}
				if evidenceProperty(t, results, "path") != "/opt/runtime" {
					t.Fatal("selected runtime path lost")
				}
				if suffix, ok := map[string]string{"python_prerelease": "rc1", "node_build_suffix": "-custom", "java_ea_suffix": "-ea"}[tt.name]; ok {
					if evidenceProperty(t, results, "version_suffix") != suffix {
						t.Fatal("version suffix evidence lost")
					}
				}
			}
		})
	}
}

func TestRuntimeCollectorUsesActualPythonVersionQuery(t *testing.T) {
	root := collectionRoot(t)
	raw, err := sandboxCommand(root, "/bin/python3", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("real Python version query: %v %s", err, raw)
	}
	results := collectInSandbox(t, root, collectionArtifact(t, "D03", map[string]any{"runtime": "python", "minimum_version": "3", "maximum_version": "4"}))
	if len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("actual Python query must pass supported version bounds: %+v", results)
	}
	if evidenceProperty(t, results, "version_raw") != string(raw) || evidenceProperty(t, results, "path") != "/bin/python3" {
		t.Fatal("actual runtime observation or resolved path changed")
	}
	for _, entry := range results[0].Evidence {
		if entry.Source != "runtime version query" || entry.StartTime == "" || entry.EndTime == "" {
			t.Fatal("runtime source or observation times lost")
		}
	}
}

func TestRuntimeCollectorReportsMissingFromSearchPath(t *testing.T) {
	for _, kind := range []string{"python", "node", "java"} {
		t.Run(kind, func(t *testing.T) {
			root := collectionRoot(t)
			// Goss resolves "sh" through $PATH, so a narrowed PATH still
			// needs a shell entry; the runtimes themselves stay hidden.
			if err := os.Symlink("/bin/sh", filepath.Join(root, "tools/sh")); err != nil {
				t.Fatal(err)
			}
			a := collectionArtifact(t, "D03", map[string]any{"runtime": kind, "minimum_version": "1"})
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := sandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json")
			for i, arg := range cmd.Args {
				if arg == "--" {
					args := append([]string{}, cmd.Args[:i]...)
					args = append(args, "--setenv", "PATH", "/tools")
					cmd.Args = append(args, cmd.Args[i:]...)
					break
				}
			}
			out, err := cmd.Output()
			if err != nil && len(out) == 0 {
				t.Fatal(err)
			}
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != "FAIL" {
				t.Fatalf("missing visible runtime must FAIL: %+v %v", results, err)
			}
			if evidenceProperty(t, results, "exists") != false {
				t.Fatal("runtime absence was not recorded")
			}
		})
	}
}

func TestRuntimeCollectorDistinguishesMissingAndAccessFailure(t *testing.T) {
	for _, tt := range []struct{ name, status string }{{"missing", "FAIL"}, {"parent_denied", "ERROR"}, {"not_executable", "ERROR"}, {"directory", "ERROR"}} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureTool(t, root, "blocked/runtime", `exit 99`)
			path := filepath.Join(root, "blocked/runtime")
			switch tt.name {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "parent_denied":
				parent := filepath.Dir(path)
				if err := os.Chmod(parent, 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
			case "not_executable":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "D03", map[string]any{"runtime": "python", "path": "/blocked/runtime", "minimum_version": "3"}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}
