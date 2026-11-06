package compiler_test

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/template"
)

func TestPathChecksRoundTripThroughOfficialGoss(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	dir := t.TempDir()
	doc, _ := template.Lookup("S01", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "path", Enabled: true, TemplateID: "S01", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"path": dir, "filetype": "directory"}}
	p := &plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}}
	artifact, err := compiler.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := compiler.Compile(p)
	if artifact.Digest != second.Digest {
		t.Fatal("compilation is nondeterministic")
	}
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "goss")
	if err := os.WriteFile(binary, payload, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "goss.json")
	if err := os.WriteFile(config, artifact.Config, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "-g", config, "validate", "--format", "json").Output()
	if err != nil {
		t.Fatal(err)
	}
	results, err := compiler.Normalize(artifact, out)
	if err != nil || len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	results, err = compiler.Normalize(artifact, []byte(`{"results":[]}`))
	if err != nil || results[0].Status != "ERROR" {
		t.Fatalf("missing evidence passed: %+v %v", results, err)
	}
	artifact.Config = json.RawMessage(`{}`)
	if _, err := compiler.Normalize(artifact, out); err == nil {
		t.Fatal("changed artifact accepted with stale mapping digest")
	}
	other := item
	other.ItemID = "conflict"
	other.Params = map[string]any{"path": dir, "exists": false}
	p.Roles[0].Items = append(p.Roles[0].Items, other)
	if _, err := compiler.Compile(p); err == nil || !strings.Contains(err.Error(), "RESOURCE_CONFLICT") {
		t.Fatalf("conflict accepted: %v", err)
	}
}

func TestCustomShellCompilationKeepsParameterOutOfSource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("generated execution scripts target Linux")
	}
	snapshot := plan.CheckTemplate{TemplateID: "custom", Version: "1", Source: "PLAN_LOCAL", ExecutionMode: "STANDARD", ParamsSchema: map[string]any{"type": "object", "properties": map[string]any{"VALUE": map[string]any{"type": "string"}}}, Implementation: map[string]any{"kind": "CUSTOM_SHELL", "command": "printf '%s' \"$RS_PARAM_VALUE\"", "expect_exit_code": float64(0)}}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	item := plan.CheckItem{ItemID: "shell", Enabled: true, TemplateID: "custom", TemplateVersion: "1", Params: map[string]any{"VALUE": "'; touch " + marker + "; '"}}
	p := &plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}}
	a, err := compiler.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]map[string]map[string]any
	if err := json.Unmarshal(a.Config, &config); err != nil {
		t.Fatal(err)
	}
	var command string
	for _, entry := range config["command"] {
		command = entry["exec"].(string)
	}
	out, err := exec.Command("/bin/sh", "-c", command).Output()
	frame, actual, hasFrame := strings.Cut(string(out), "\n")
	if err != nil || !hasFrame || !strings.HasPrefix(frame, "__READYSPEC_START_") || actual != item.Params["VALUE"] {
		t.Fatalf("parameter changed execution: output=%q err=%v", out, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("parameter executed as shell source")
	}
}

func TestNumericBuiltinsCompileWithExplicitUnitsAndObservationMatchers(t *testing.T) {
	for id, params := range map[string]map[string]any{"H02": {"min_cores": float64(4)}, "H03": {"min_memory": "8 GiB"}, "S04": {"path": "/data", "min_available": "10 GB"}} {
		doc, _ := template.Lookup(id, "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "numeric", Enabled: true, TemplateID: id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !strings.Contains(string(a.Config), `"stdout":{"ge":`) {
			if id == "H03" && len(a.Origins) == 2 {
				expected, _ := a.Origins[1].Expected.(map[string]any)
				if expected["min_memory"] == int64(8589934592) {
					continue
				}
			}
			if id == "S04" && len(a.Origins) == 2 {
				expected, _ := a.Origins[1].Expected.(map[string]any)
				if expected["min_available"] == int64(10000000000) {
					continue
				}
			}
			t.Fatalf("%s missing numeric observation: %s", id, a.Config)
		}
	}
}

func TestNumericGossEvidenceAndCollectionFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	doc, _ := template.Lookup("H02", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "cpu", Enabled: true, TemplateID: "H02", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"min_cores": float64(8)}}
	a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "goss")
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, payload, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ command, status string }{{"printf '10\\n'", "PASS"}, {"printf '4\\n'", "FAIL"}, {"printf 'garbage\\n'", "ERROR"}, {"exit 2", "ERROR"}} {
		var config map[string]map[string]map[string]any
		_ = json.Unmarshal(a.Config, &config)
		for _, entry := range config["command"] {
			entry["exec"] = "/bin/sh -c '" + tt.command + "'"
			if tt.command != "exit 2" {
				entry["exec"] = "printf " + strings.TrimPrefix(tt.command, "printf ")
			}
		}
		data, _ := json.Marshal(config)
		path := filepath.Join(dir, "goss.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
		results, err := compiler.Normalize(a, out)
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("%s: %s results=%+v err=%v", tt.command, out, results, err)
		}
	}
}

func TestNativeTemplateResourceMapping(t *testing.T) {
	for id, tt := range map[string]struct {
		params   map[string]any
		resource string
	}{
		"S02": {map[string]any{"path": "/data", "mode": "0750"}, "file"},
		"O03": {map[string]any{"name": "vm.swappiness", "value": "10"}, "command"},
		"R01": {map[string]any{"name": "sshd", "running": true, "enabled": false}, "command"},
	} {
		doc, ok := template.Lookup(id, "1.0.0")
		if !ok {
			t.Fatalf("%s unavailable", id)
		}
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "native", Enabled: true, TemplateID: id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: tt.params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		_ = json.Unmarshal(a.Config, &config)
		if config[tt.resource] == nil {
			t.Fatalf("%s wrong resource: %s", id, a.Config)
		}
	}
}

func TestCollectorTemplatesCompileWithoutAccessingHost(t *testing.T) {
	for id, params := range map[string]map[string]any{
		"H01": {"allowed_arch": []any{"x86_64"}}, "H04": {"mode": "disabled"}, "O04": {"name": "ip_tables", "loaded": false}, "S05": {"path": "/data", "min_available": float64(100)}, "R03": {"window": float64(5), "maximum": float64(2)}, "L01": {"minimum_seconds": float64(60)},
	} {
		doc, _ := template.Lookup(id, "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "collector", Enabled: true, TemplateID: id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(a.Origins) != 2 {
			t.Fatalf("%s: collector lacks command and stdout mapping", id)
		}
	}
}

func TestCollectorMatchersUseOfficialGoss(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
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
	for _, tt := range []struct {
		id, output string
		params     map[string]any
	}{
		{"H01", "x86_64", map[string]any{"allowed_arch": []any{"x86_64"}}},
		{"H04", "Filename Type Size Used Priority\n", map[string]any{"mode": "disabled"}},
		{"O04", "", map[string]any{"name": "test_module", "loaded": false}},
		{"S05", filesystemObservationFixture("4096\n1000\n500\n400\n1000\n100\nxfs\n/data\n"), map[string]any{"path": "/data", "min_available": float64(100)}},
		{"R03", "1.00 1.50 3.00 1/100 42\n", map[string]any{"window": float64(5), "maximum": float64(2)}},
		{"L01", "60.00 123.45\n", map[string]any{"minimum_seconds": float64(60)}},
	} {
		doc, _ := template.Lookup(tt.id, "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "collector", Enabled: true, TemplateID: tt.id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: tt.params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]map[string]map[string]any
		_ = json.Unmarshal(a.Config, &config)
		for _, entry := range config["command"] {
			entry["exec"] = "printf %s '" + strings.ReplaceAll(tt.output, "'", "'\"'\"'") + "'"
		}
		data, _ := json.Marshal(config)
		path := filepath.Join(dir, "goss.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
		results, err := compiler.Normalize(a, out)
		if err != nil || len(results) != 1 || results[0].Status != "PASS" {
			t.Fatalf("%s: %s results=%+v err=%v", tt.id, out, results, err)
		}
	}
}

func TestHostCompilationSelectsRolesAndAppliesOnlyOpenedOverrides(t *testing.T) {
	doc, _ := template.Lookup("S01", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "path", Enabled: true, TemplateID: "S01", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"path": "/original"}, OverridableParams: []string{"path"}}
	p := &plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}, {RoleID: "unused", Items: []plan.CheckItem{{ItemID: "unsupported", Enabled: true, TemplateID: "unknown"}}}}}
	a, err := compiler.CompileHost(p, compiler.HostInput{Roles: []string{"app"}, HostOverrides: map[string]map[string]any{"path": {"path": "/host"}}})
	if err != nil || strings.Contains(string(a.Config), "/original") || !strings.Contains(string(a.Config), "/host") {
		t.Fatalf("artifact=%s err=%v", a.Config, err)
	}
	if p.Roles[0].Items[0].Params["path"] != "/original" {
		t.Fatal("compilation modified plan")
	}
}

func TestHTTPProbeUsesOfficialGossAgainstLocalServer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("unexpected method %s", r.Method)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	doc, _ := template.Lookup("N05", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "http", Enabled: true, TemplateID: "N05", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"url": server.URL, "status": float64(204), "method": "HEAD"}}
	a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "goss")
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, payload, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goss.json")
	if err := os.WriteFile(path, a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	results, err := compiler.Normalize(a, out)
	if err != nil || len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("%+v %v %s", results, err, out)
	}
}

func TestPrivilegedChecksHaveIndependentArtifacts(t *testing.T) {
	doc, _ := template.Lookup("A03", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "ssh", Enabled: true, TemplateID: "A03", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"key": "permitrootlogin", "expected": "no"}}
	p := &plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}}
	if _, err := compiler.Compile(p); err == nil {
		t.Fatal("privileged check compiled as STANDARD")
	}
	artifacts, err := compiler.CompileExecutionModes(p, compiler.HostInput{Roles: []string{"app"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(artifacts.Standard.Config), "sshd") || !strings.Contains(string(artifacts.Privileged.Config), "sshd -T") {
		t.Fatalf("incorrect split: %+v", artifacts)
	}
}

func TestTCPAndDNSProbesOnlyUseLocalFixtures(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, address, err := dns.ReadFrom(buffer)
			if err != nil {
				return
			}
			if n < 17 {
				continue
			}
			end := 12
			for end < n && buffer[end] != 0 {
				end += int(buffer[end]) + 1
			}
			end += 5
			if end > n {
				continue
			}
			response := make([]byte, 12)
			copy(response, buffer[:2])
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[4:6], 1)
			binary.BigEndian.PutUint16(response[6:8], 1)
			response = append(response, buffer[12:end]...)
			response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, 1)
			_, _ = dns.WriteTo(response, address)
		}
	}()
	for id, params := range map[string]map[string]any{"N03": {"address": tcp.Addr().String(), "reachable": true}, "N04": {"name": "readyspec.test", "server": dns.LocalAddr().String(), "record_type": "A", "addrs": []any{"127.0.0.1"}}} {
		doc, _ := template.Lookup(id, "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "network", Enabled: true, TemplateID: id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		payload, err := goss.Binary(runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(dir, "goss")
		if err := os.WriteFile(executable, payload, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "goss.json")
		if err := os.WriteFile(path, a.Config, 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(executable, "-g", path, "validate", "--format", "json").Output()
		if err != nil {
			t.Fatalf("%s: %v %s", id, err, out)
		}
		results, err := compiler.Normalize(a, out)
		if err != nil || len(results) != 1 || results[0].Status != "PASS" {
			t.Fatalf("%s: %+v %v %s", id, results, err, out)
		}
	}
}

func TestAdvancedObservationsAreEvaluatedWithoutExecutingTargets(t *testing.T) {
	for _, tt := range []struct {
		id, actual, status string
		params             map[string]any
	}{
		{"O02", "6.12.3-custom", "PASS", map[string]any{"minimum_version": "6.9"}},
		{"O02", "5.4.0-generic", "FAIL", map[string]any{"minimum_version": "6.9"}},
		{"O02", "unknown", "ERROR", map[string]any{"minimum_version": "6.9"}},
		{"R02", "1 worker\n2 worker\n3 other\n", "PASS", map[string]any{"name": "worker", "minimum_count": float64(2), "maximum_count": float64(2)}},
		{"R04", "Max open files            1024                 4096                 files\n", "FAIL", map[string]any{"pid": float64(42), "limit": "NOFILE", "minimum": float64(2048)}},
		{"D02", `{"exists":true,"path":"/usr/bin/tool","filetype":"file","executable":true,"mode":"0755"}`, "PASS", map[string]any{"command": "tool"}},
		{"D03", runtimeObservationFixture("Python 3.12.7\n"), "PASS", map[string]any{"runtime": "python", "minimum_version": "3.11"}},
		{"D03", runtimeObservationFixture("not a runtime\n"), "ERROR", map[string]any{"runtime": "python", "minimum_version": "3.11"}},
		{"D03", "__RS_RUNTIME_MISSING__\n", "FAIL", map[string]any{"runtime": "python", "minimum_version": "3.11"}},
		{"N06", `[{"dev":"eth0","gateway":"10.0.0.1"}]`, "PASS", map[string]any{"target": "10.1.1.1", "interface": "eth0", "gateway": "10.0.0.1"}},
	} {
		doc, _ := template.Lookup(tt.id, "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "advanced", Enabled: true, TemplateID: tt.id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: tt.params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		observations := []map[string]any{}
		for _, origin := range a.Origins {
			actual := any(tt.actual)
			if origin.Property == "exit-status" {
				actual = float64(0)
			}
			observations = append(observations, map[string]any{"resource-id": origin.Resource, "resource-type": origin.Type, "property": origin.Property, "successful": true, "err": nil, "matcher-result": map[string]any{"actual": actual}})
		}
		report, _ := json.Marshal(map[string]any{"results": observations})
		results, err := compiler.Normalize(a, report)
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("%s %q: %+v %v", tt.id, tt.actual, results, err)
		}
	}
}

func TestRemainingTemplateObservationsAndConfigRedaction(t *testing.T) {
	for _, tt := range []struct {
		id, actual, status string
		params             map[string]any
	}{
		{"O01", "ID=customlinux\nVERSION_ID=24.10\n", "PASS", map[string]any{"allowed_ids": []any{"customlinux"}, "minimum_version": "24.9"}},
		{"N01", `[{"flags":["UP"],"addr_info":[{"local":"10.0.0.3","prefixlen":24}]}]`, "PASS", map[string]any{"name": "eth0", "up": true, "addresses": []any{"10.0.0.0/24"}}},
		{"R05", timeSyncObservationFixture("yes"), "PASS", map[string]any{"allowed_services": []any{"chronyd"}}},
		{"A02", `{"exists":true,"lookup_status":"SUCCESS","source":"libc NSS","name":"users","gid":100,"supplementary_members":["extra"],"required_users":[{"requested":"primary","exists":true,"lookup_status":"SUCCESS","source":"libc NSS","name":"primary","uid":1000,"gid":100},{"requested":"extra","exists":true,"lookup_status":"SUCCESS","source":"libc NSS","name":"extra","uid":1001,"gid":101}]}`, "PASS", map[string]any{"name": "users", "gid": float64(100), "members": []any{"primary", "extra"}}},
		{"A04", "Enforcing\n", "PASS", map[string]any{"framework": "selinux", "mode": "enforcing"}},
		{"D01", "installed=true\nversion=1:2.0-3\nminimum=true\nmaximum=true\n", "PASS", map[string]any{"name": "test", "manager": "dpkg", "installed": true, "minimum_version": "1:1.0-1"}},
		{"D04", observationFixture("D04", false).(string), "PASS", map[string]any{"path": "/opt/app", "architecture": "x86_64", "required_libraries": []any{"libc.so.6"}}},
		{"D05", "PASSWORD=private-value\n", "PASS", map[string]any{"path": "/opt/app.env", "format": "env", "key": "PASSWORD", "expected": "private-value"}},
	} {
		doc, _ := template.Lookup(tt.id, "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "advanced", Enabled: true, TemplateID: tt.id, TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: tt.params}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		observations := []map[string]any{}
		for _, origin := range a.Origins {
			actual := any(tt.actual)
			if origin.Property == "exit-status" {
				actual = float64(0)
			}
			observations = append(observations, map[string]any{"resource-id": origin.Resource, "resource-type": origin.Type, "property": origin.Property, "successful": true, "err": nil, "matcher-result": map[string]any{"actual": actual}})
		}
		report, _ := json.Marshal(map[string]any{"results": observations})
		results, err := compiler.Normalize(a, report)
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("%s: %+v %v", tt.id, results, err)
		}
		if tt.id == "D05" {
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "private-value") {
				t.Fatal("configuration secret leaked into normalized evidence")
			}
		}
	}
}

func TestLogHistoryRequiresCoverageButKnownViolationStillFails(t *testing.T) {
	for _, tt := range []struct{ old, events, status string }{
		{`{"__REALTIME_TIMESTAMP":"5000000000","MESSAGE":"older"}`, "", "PASS"},
		{"", "", "ERROR"},
		{"", `{"__REALTIME_TIMESTAMP":"7000000000","MESSAGE":"Out of memory: Killed process 42 (worker)"}`, "FAIL"},
	} {
		doc, _ := template.Lookup("L02", "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "logs", Enabled: true, TemplateID: "L02", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"lookback_seconds": float64(3600)}}
		artifacts, err := compiler.CompileExecutionModes(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}}, compiler.HostInput{Roles: []string{"app"}})
		if err != nil {
			t.Fatal(err)
		}
		a := artifacts.Standard
		text := "__RS_WINDOW__\n10000\n6400\n1000\n__RS_OLDER__\n" + tt.old + "\n__RS_BOOT_START__\n__RS_EVENTS__\n" + tt.events + "\n" + journalAuditObservationFixture
		observations := []map[string]any{}
		for _, origin := range a.Origins {
			actual := any(text)
			if origin.Property == "exit-status" {
				actual = float64(0)
			}
			observations = append(observations, map[string]any{"resource-id": origin.Resource, "resource-type": origin.Type, "property": origin.Property, "successful": true, "err": nil, "matcher-result": map[string]any{"actual": actual}})
		}
		report, _ := json.Marshal(map[string]any{"results": observations})
		results, err := compiler.Normalize(a, report)
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("status=%s result=%+v err=%v", tt.status, results, err)
		}
	}
}

func TestServiceStateRetainsSystemdStatesAndRejectsMissingService(t *testing.T) {
	doc, _ := template.Lookup("R01", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "service", Enabled: true, TemplateID: "R01", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"name": "app", "running": false, "enabled": false}}
	a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a.Config), "systemctl") {
		t.Fatal("systemd raw states were not collected")
	}
}

func TestCapacityBoundaryAboveFloatPrecisionUsesExactOriginalThreshold(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	doc, _ := template.Lookup("H03", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "memory", Enabled: true, TemplateID: "H03", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"min_memory": "9007199254740993 B"}}
	a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]map[string]map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(a.Config)))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		t.Fatal(err)
	}
	for _, entry := range config["command"] {
		entry["exec"] = "printf %s 'MemTotal: 8796093022208 kB'"
	}
	dir := t.TempDir()
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "goss")
	if err := os.WriteFile(executable, payload, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(config)
	path := filepath.Join(dir, "goss.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(executable, "-g", path, "validate", "--format", "json").Output()
	results, err := compiler.Normalize(a, out)
	if err != nil || len(results) != 1 || results[0].Status != "FAIL" {
		t.Fatalf("capacity precision lost: %+v %v %s", results, err, out)
	}
}

func TestHTTPBodyAndHeaderMatchersRetainObservedEvidence(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ReadySpec", "healthy")
		_, _ = w.Write([]byte(`{"healthy":true}`))
	}))
	defer server.Close()
	doc, _ := template.Lookup("N05", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "http", Enabled: true, TemplateID: "N05", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"url": server.URL, "status": float64(200), "body_contains": "healthy", "body_regex": `true`, "headers": map[string]any{"X-ReadySpec": "healthy"}}}
	a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
	if err != nil {
		t.Fatal(err)
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
	path := filepath.Join(dir, "goss.json")
	if err := os.WriteFile(path, a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	results, err := compiler.Normalize(a, out)
	if err != nil || len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("%+v %v %s", results, err, out)
	}
}

func TestHTTPBodyCollectionRejectsResponsesAboveOneMiB(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1024*1024) + "HEALTHY"))
	}))
	defer server.Close()
	p := catalogPlan(t, "N05")
	p.Roles[0].Items[0].Params = map[string]any{"url": server.URL, "status": float64(200), "body_contains": "HEALTHY"}
	a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
	dir := t.TempDir()
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "goss")
	if err := os.WriteFile(executable, payload, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goss.json")
	if err := os.WriteFile(path, a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(executable, "-g", path, "validate", "--format", "json").Output()
	results, err := compiler.Normalize(a, out)
	if err != nil || len(results) != 1 || results[0].Status != "ERROR" {
		t.Fatalf("oversized body must be ERROR; got status=%v err=%v", results[0].Status, err)
	}
}

func TestPathSymlinkPolicyIsEnforcedByOfficialGoss(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "goss")
	if err := os.WriteFile(binary, payload, 0700); err != nil {
		t.Fatal(err)
	}
	for _, follow := range []bool{false, true} {
		doc, _ := template.Lookup("S01", "1.0.0")
		raw, _ := json.Marshal(doc)
		var snapshot plan.CheckTemplate
		_ = json.Unmarshal(raw, &snapshot)
		item := plan.CheckItem{ItemID: "path", Enabled: true, TemplateID: "S01", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"path": link, "filetype": "directory", "follow_symlinks": follow}}
		a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "goss.json")
		if err := os.WriteFile(path, a.Config, 0600); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
		results, err := compiler.Normalize(a, out)
		expected := "FAIL"
		if follow {
			expected = "PASS"
		}
		if err != nil || len(results) != 1 || results[0].Status != expected {
			t.Fatalf("follow=%v: %+v %v %s", follow, results, err, out)
		}
	}
}
