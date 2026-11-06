package compiler_test

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/template"
)

func catalogSamples() map[string]map[string]any {
	return map[string]map[string]any{
		"H01": {"allowed_arch": []any{"x86_64"}}, "H02": {"min_cores": float64(2)}, "H03": {"min_memory": "8 GiB"}, "H04": {"mode": "disabled"},
		"O01": {"allowed_ids": []any{"custom"}}, "O02": {"minimum_version": "5.10"}, "O03": {"name": "vm.swappiness", "value": "10"}, "O04": {"name": "overlay", "loaded": true},
		"S01": {"path": "/data", "filetype": "directory"}, "S02": {"path": "/data", "mode": "0750"}, "S03": {"path": "/data", "filesystem": "xfs", "required_options": []any{"rw"}, "forbidden_options": []any{"noexec"}}, "S04": {"path": "/data", "min_available": "1 GB"}, "S05": {"path": "/data", "min_available": float64(100)},
		"N01": {"name": "eth0", "up": true, "addresses": []any{"10.0.0.0/24"}}, "N02": {"protocol": "tcp", "port": float64(8080), "listening": false}, "N03": {"address": "127.0.0.1:80", "reachable": true}, "N04": {"name": "example.test", "server": "127.0.0.1:53", "record_type": "A", "addrs": []any{"127.0.0.1"}}, "N05": {"url": "http://127.0.0.1", "status": float64(200)}, "N06": {"target": "10.0.0.1", "interface": "eth0"},
		"R01": {"name": "sshd", "enabled": true, "running": true}, "R02": {"name": "worker", "minimum_count": float64(1)}, "R03": {"window": float64(5), "maximum": float64(2)}, "R04": {"pid": float64(42), "limit": "NOFILE", "minimum": float64(1024)}, "R05": {"allowed_services": []any{"chronyd"}},
		"A01": {"name": "app", "exists": true, "uid": float64(1000)}, "A02": {"name": "app", "members": []any{"app"}}, "A03": {"key": "permitrootlogin", "expected": "no"}, "A04": {"framework": "selinux", "mode": "enforcing"},
		"D01": {"name": "app", "manager": "rpm", "installed": true, "minimum_version": "1:2.0-1", "maximum_version": "2:1.0-1"}, "D02": {"command": "myapp"}, "D03": {"runtime": "python", "minimum_version": "3.11"}, "D04": {"path": "/opt/app", "architecture": "x86_64", "required_libraries": []any{"libc.so.6"}}, "D05": {"path": "/opt/app.env", "format": "env", "key": "DATA_PATH", "expected": "/data"},
		"L01": {"minimum_seconds": float64(60)}, "L02": {"lookback_seconds": float64(3600)}, "L03": {"unit": "app.service", "lookback_seconds": float64(3600)},
	}
}

func TestAllCatalogTemplatesCompileAndGeneratedShellParses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("generated execution scripts target Linux")
	}
	samples := catalogSamples()
	ids := map[string]bool{}
	for _, entry := range template.List() {
		ids[entry["template_id"].(string)] = true
	}
	if len(ids) != 36 {
		t.Fatalf("registered families=%d", len(ids))
	}
	for _, definition := range template.List() {
		id := definition["template_id"].(string)
		version := definition["version"].(string)
		t.Run(id+"@"+version, func(t *testing.T) {
			params, exists := samples[id]
			if id == "R04" && version == "1.0.0-config" {
				params = map[string]any{"unit": "app.service", "limit": "NOFILE", "minimum": float64(1024)}
			}
			if !exists {
				t.Fatal("missing independent sample")
			}
			raw, _ := json.Marshal(definition)
			var snapshot plan.CheckTemplate
			_ = json.Unmarshal(raw, &snapshot)
			item := plan.CheckItem{ItemID: "sample", Enabled: true, TemplateID: id, TemplateVersion: version, TemplateDigest: snapshot.Digest, Params: params}
			p := &plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}}
			artifacts, err := compiler.CompileExecutionModes(p, compiler.HostInput{Roles: []string{"app"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, artifact := range []compiler.Artifact{artifacts.Standard, artifacts.Privileged} {
				var config map[string]map[string]map[string]any
				if err := json.Unmarshal(artifact.Config, &config); err != nil {
					t.Fatal(err)
				}
				for _, command := range config["command"] {
					script := command["exec"].(string)
					if !strings.HasPrefix(script, "/bin/sh -c ") {
						t.Fatal("unexpected command wrapper")
					}
					syntaxOnly := strings.Replace(script, "/bin/sh -c ", "/bin/sh -n -c ", 1)
					if out, err := exec.Command("/bin/sh", "-c", syntaxOnly).CombinedOutput(); err != nil {
						t.Fatalf("invalid generated shell: %v %s", err, out)
					}
				}
			}
		})
	}
}
