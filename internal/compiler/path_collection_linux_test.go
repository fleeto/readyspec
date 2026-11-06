//go:build linux

package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPathCollectorPreservesSymlinkTargetEvidence(t *testing.T) {
	for _, follow := range []bool{false, true} {
		for _, kind := range []string{"file", "directory", "dangling", "denied", "loop", "newline_target", "absolute_target", "link_chain"} {
			t.Run(fmt.Sprintf("follow_%v/%s", follow, kind), func(t *testing.T) {
				root := collectionRoot(t)
				target := "target"
				switch kind {
				case "absolute_target":
					target = "/target"
					fixtureFile(t, root, "target", "data")
				case "link_chain":
					target = "second"
					fixtureFile(t, root, "target", "data")
					if err := os.Symlink("target", filepath.Join(root, "second")); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(filepath.Join(root, target), 0755); err != nil {
						t.Fatal(err)
					}
				case "dangling":
					target = "missing"
				case "denied":
					target = "blocked/target"
					fixtureFile(t, root, target, "data")
					parent := filepath.Join(root, "blocked")
					if err := os.Chmod(parent, 0000); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
				case "loop":
					target = "link"
				case "newline_target":
					target = "target\nname"
					fixtureFile(t, root, target, "data")
				default:
					fixtureFile(t, root, target, "data")
				}
				if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
				params := map[string]any{"path": "/link", "exists": true, "follow_symlinks": follow}
				want := "PASS"
				if follow && (kind == "denied" || kind == "loop") {
					want = "ERROR"
				} else if kind == "dangling" && follow {
					want = "FAIL"
				}
				results := collectInSandbox(t, root, collectionArtifact(t, "S01", params))
				if len(results) != 1 || results[0].Status != want {
					t.Fatalf("want %s got %+v", want, results)
				}
				if actual := evidenceProperty(t, results, "link_target"); actual != target {
					t.Fatalf("target want %q got %q", target, actual)
				}
				if evidenceProperty(t, results, "node_type") != "symlink" {
					t.Fatal("link node type lost")
				}
				if evidenceProperty(t, results, "follow_symlinks") != follow {
					t.Fatal("follow policy lost")
				}
				if kind == "denied" || kind == "loop" {
					expected := "EACCES"
					if kind == "loop" {
						expected = "ELOOP"
					}
					if actual := evidenceProperty(t, results, "target_error"); actual != expected {
						t.Fatalf("want target error %s got %v", expected, actual)
					}
				}
				if kind != "denied" && kind != "loop" {
					exists := kind != "dangling"
					if evidenceProperty(t, results, "target_exists") != exists {
						t.Fatal("target existence lost")
					}
					if exists {
						expected := "file"
						if kind == "directory" {
							expected = "directory"
						}
						if evidenceProperty(t, results, "target_type") != expected {
							t.Fatal("target type lost")
						}
					}
				}
			})
		}
	}
}

func TestPathCollectorMetadataForDirectNodesAndAbsentPaths(t *testing.T) {
	for _, kind := range []string{"file", "directory", "absent"} {
		t.Run(kind, func(t *testing.T) {
			root := collectionRoot(t)
			if kind == "file" {
				fixtureFile(t, root, "node", "data")
			} else if kind == "directory" {
				if err := os.Mkdir(filepath.Join(root, "node"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			params := map[string]any{"path": "/node", "exists": kind != "absent"}
			if kind != "absent" {
				params["filetype"] = kind
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "S01", params))
			if len(results) != 1 || results[0].Status != "PASS" {
				t.Fatalf("got %+v", results)
			}
			if evidenceProperty(t, results, "node_exists") != (kind != "absent") {
				t.Fatal("node existence metadata lost")
			}
			if kind != "absent" && evidenceProperty(t, results, "node_type") != kind {
				t.Fatal("node type metadata lost")
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "/node" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("source or collection times missing")
				}
			}
		})
	}
}
