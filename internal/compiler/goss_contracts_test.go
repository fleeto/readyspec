package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
	"github.com/fleeto/readyspec/internal/template"
)

// Exercise each generated matcher against independent fixture observations in
// the official Goss binary. Matching resources avoid inspecting the developer's
// actual services, users, package database, journal or hardware.
func TestAllTemplateMatchersRoundTripOfficialGoss(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official Goss targets Linux")
	}
	dir := t.TempDir()
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "goss")
	if err := os.WriteFile(binary, payload, 0700); err != nil {
		t.Fatal(err)
	}
	resourceNames := map[string]string{"Command": "command", "File": "file", "KernelParam": "kernel-param", "User": "user", "Mount": "mount", "Port": "port", "Addr": "addr", "DNS": "dns", "HTTP": "http"}
	for _, entry := range template.List() {
		if entry["version"] != "1.0.0" {
			continue
		}
		id := entry["template_id"].(string)
		t.Run(id, func(t *testing.T) {
			p := catalogPlan(t, id)
			a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
			var compiled map[string]map[string]map[string]any
			if err := json.Unmarshal(a.Config, &compiled); err != nil {
				t.Fatal(err)
			}
			matching := map[string]any{}
			origins := map[string]compiler.Origin{}
			for _, negative := range []bool{false, true} {
				for i, origin := range a.Origins {
					actual := observationFixture(id, negative)
					if fields, ok := actual.(map[string]any); ok {
						actual = fields[origin.Property]
					}
					if origin.Evaluator == "N04_RCODE" {
						actual = `{"rcode":0,"truncated":false,"timed_out":false,"duration_ms":1}`
					}
					if origin.Collection {
						actual = float64(0)
					}
					matcher := compiled[resourceNames[origin.Type]][origin.Resource][origin.Property]
					if matcher == nil {
						t.Fatalf("missing compiled matcher: %+v", origin)
					}
					key := fmt.Sprintf("probe-%d", i)
					origins[key] = origin
					matching[key] = map[string]any{"content": actual, "matches": matcher}
				}
				raw, _ := json.Marshal(map[string]any{"matching": matching})
				path := filepath.Join(dir, "goss.json")
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
				if err != nil && !negative {
					t.Fatalf("%v: %s", err, out)
				}
				var report map[string]any
				if err := json.Unmarshal(out, &report); err != nil {
					t.Fatal(err)
				}
				for _, value := range report["results"].([]any) {
					result := value.(map[string]any)
					origin := origins[result["resource-id"].(string)]
					result["resource-id"] = origin.Resource
					result["resource-type"] = origin.Type
					result["property"] = origin.Property
				}
				out, _ = json.Marshal(report)
				results, err := compiler.Normalize(a, out)
				expected := "PASS"
				if negative {
					expected = "FAIL"
				}
				if err != nil || len(results) != 1 || results[0].Status != expected {
					t.Fatalf("%+v %v output=%s", results, err, out)
				}
			}
		})
	}
}
