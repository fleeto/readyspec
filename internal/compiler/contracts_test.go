package compiler_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/template"
)

// These fixtures exercise public compiler/normalizer contracts, independently
// of target collection. Official-Goss execution tests are kept separately.
func catalogPlan(t *testing.T, id string) *plan.Plan {
	t.Helper()
	snapshot, ok := template.Lookup(id, "1.0.0")
	if !ok {
		t.Fatal("missing template")
	}
	item := plan.CheckItem{ItemID: "sample", Name: "Sample", Enabled: true, Importance: "REQUIRED", TemplateID: id, TemplateVersion: "1.0.0", TemplateDigest: snapshot["digest"].(string), Params: catalogSamples()[id], ExplicitNullParams: []string{}, OverridableParams: []string{}}
	doc := map[string]any{"schema_version": "readyspec.plan/v1", "plan_id": "contract", "name": "Contract", "software_name": "fixture", "software_version": "1", "version": "1", "state": "DRAFT", "topology_policy": map[string]any{"allowed_arch": []string{"x86_64"}}, "roles": []plan.Role{{RoleID: "app", Name: "App", MinNodes: 1, Items: []plan.CheckItem{item}}}, "embedded_templates": []any{snapshot}, "template_lock": map[string]any{id + "@1.0.0": snapshot["digest"]}}
	raw, _ := json.Marshal(doc)
	p, err := plan.Load(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("%s strict validation: %v", id, err)
	}
	return p
}

func selectedArtifact(t *testing.T, p *plan.Plan, input compiler.HostInput) compiler.Artifact {
	t.Helper()
	all, err := compiler.CompileExecutionModes(p, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Privileged.Items) > 0 {
		return all.Privileged
	}
	return all.Standard
}

func filesystemObservationFixture(text string) string {
	return text + "__RS_MOUNTINFO__\n24 1 8:1 / /data rw - xfs /dev/fixture rw\n"
}

const journalAuditObservationFixture = "__RS_AUDIT_OLDER__\n" + `{"__REALTIME_TIMESTAMP":"5000000000","__CURSOR":"s=0123456789abcdef0123456789abcdef;i=a"}` + "\n__RS_AUDIT__\n" + `{"__REALTIME_TIMESTAMP":"7000000000","__CURSOR":"s=0123456789abcdef0123456789abcdef;i=b","MESSAGE":"ordinary activity"}` + "\n__RS_AUDIT_END__\n"

func timeSyncObservationFixture(sync string) string {
	return "__RS_TIME_SYNC__\n" + sync + "\n\n__RS_TIME_SYNC_RC__\n0\n__RS_SERVICE__\nchronyd\nactive\n\n__RS_SERVICE_RC__\n0\n__RS_TIME_END__\n"
}

func runtimeObservationFixture(version string) string {
	return "__RS_RUNTIME_PATH__\n/bin/python3\n__RS_RUNTIME_VERSION__\n" + version + "\n__RS_RUNTIME_RC__\n0\n"
}

func observationFixture(id string, negative bool) any {
	if id == "D02" {
		observed := `{"exists":true,"path":"/usr/bin/myapp","filetype":"file","executable":true,"mode":"0755"}`
		if negative {
			observed = `{"exists":false,"executable":false}`
		}
		return map[string]any{"stdout": observed}
	}
	if id == "S01" {
		return map[string]any{"exists": !negative, "filetype": "directory", "stdout": `{"follow_symlinks":false,"node_exists":true,"node_type":"directory"}`}
	}
	if id == "N05" {
		status := 200
		if negative {
			status = 500
		}
		raw, _ := json.Marshal(map[string]any{"status": status, "headers": map[string]any{}, "body_base64": "", "header_bytes": 128, "redirects": 0, "duration_ms": 1})
		return string(raw)
	}
	if id == "D04" {
		machine := 62
		if negative {
			machine = 183
		}
		raw, _ := json.Marshal(map[string]any{"header": map[string]any{"class": 64, "byte_order": "little", "machine": machine, "type": 3, "pie": true}, "needed": []string{"libc.so.6"}, "readelf": "ELF64 fixture observation", "cache_queried": true, "cache_entries": []any{map[string]any{"name": "libc.so.6", "flags": "libc6,x86-64", "path": "/lib/libc.so.6", "status": "OK", "header": map[string]any{"class": 64, "byte_order": "little", "machine": 62, "type": 3}}}})
		return string(raw)
	}
	positive := map[string]any{
		"H01": "x86_64", "H02": "2", "H03": "MemTotal: 8388608 kB\n", "H04": "Filename Type Size Used Priority\n",
		"O01": "ID=custom\nVERSION_ID=1\n", "O02": "5.10.0-custom", "O03": "__RS_SYSCTL_VALUE__\n10", "O04": "overlay 8192 0 - Live 0x0\n",
		"S01": map[string]any{"exists": true, "filetype": "directory"}, "S02": map[string]any{"exists": true, "mode": "0750", "uid": 1000, "gid": 1000, "owner": "app", "group": "app"}, "S03": map[string]any{"exists": true, "filesystem": "xfs", "opts": []string{"rw"}, "source": "/dev/fixture"}, "S04": "1000\n2000000\n1500000\n1000000\n1000\n100\nxfs\n/data\n", "S05": "4096\n1000\n500\n400\n1000\n100\nxfs\n/data\n",
		"N01": `[{"flags":["UP"],"addr_info":[{"local":"10.0.0.1","prefixlen":24}]}]`, "N02": "__RS_IPV4__\n__RS_IPV6__\n", "N03": `{"reachable":true,"reason":"CONNECTED","duration_ms":1}`, "N04": map[string]any{"resolvable": true, "addrs": []string{"127.0.0.1"}}, "N06": `[{"dev":"eth0"}]`,
		"R01": "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n", "R02": "1 worker\n", "R03": "1.00 2.00 3.00 1/100 42\n", "R04": "Max open files 1024 4096 files\n", "R05": timeSyncObservationFixture("yes"),
		"A01": `{"exists":true,"lookup_status":"SUCCESS","source":"libc NSS","name":"app","uid":1000,"gid":1000,"shell":"/bin/sh"}`, "A02": `{"exists":true,"lookup_status":"SUCCESS","source":"libc NSS","name":"app","gid":1000,"supplementary_members":["app"],"required_users":[{"requested":"app","exists":true,"lookup_status":"SUCCESS","source":"libc NSS","name":"app","uid":1000,"gid":1001}]}`, "A03": "permitrootlogin no\n", "A04": "Enforcing",
		"D01": "installed=true\nversion=1:2.0-1\nminimum=true\nmaximum=true\n", "D02": "exists=true\npath=/usr/bin/myapp\nmode=755\nexecutable=true\n", "D03": runtimeObservationFixture("Python 3.11.0\n"), "D05": "DATA_PATH=/data\n",
		"L01": "60.00 123.45\n", "L02": "__RS_WINDOW__\n10000\n6400\n1000\n__RS_OLDER__\n{\"__REALTIME_TIMESTAMP\":\"5000000000\",\"MESSAGE\":\"older\"}\n__RS_BOOT_START__\n__RS_EVENTS__\n", "L03": "__RS_WINDOW__\n10000\n6400\n1000\n__RS_OLDER__\n{\"__REALTIME_TIMESTAMP\":\"5000000000\",\"MESSAGE\":\"older\"}\n__RS_BOOT_START__\n__RS_EVENTS__\n",
	}
	if !negative {
		if id == "L02" || id == "L03" {
			return positive[id].(string) + journalAuditObservationFixture
		}
		if id == "S04" || id == "S05" {
			return filesystemObservationFixture(positive[id].(string))
		}
		return positive[id]
	}
	negativeValues := map[string]any{
		"H01": "aarch64", "H02": "1", "H03": "MemTotal: 1 kB\n", "H04": "Filename Type Size Used Priority\n/swap file 1 0 -2\n", "O01": "ID=other\nVERSION_ID=1\n", "O02": "4.19.0", "O03": "__RS_SYSCTL_VALUE__\n60", "O04": "",
		"S01": map[string]any{"exists": false, "filetype": "file"}, "S02": map[string]any{"exists": true, "mode": "0777", "uid": 1000, "gid": 1000, "owner": "app", "group": "app"}, "S03": map[string]any{"exists": true, "filesystem": "ext4", "opts": []string{"noexec"}, "source": "/dev/fixture"}, "S04": "1000\n2000000\n1500000\n1\n1000\n100\nxfs\n/data\n", "S05": "4096\n1000\n500\n400\n1000\n1\nxfs\n/data\n",
		"N01": `[{"flags":[],"addr_info":[]}]`, "N02": "__RS_IPV4__\nLISTEN 0 128 0.0.0.0:8080 0.0.0.0:*\n__RS_IPV6__\n", "N03": `{"reachable":false,"reason":"REFUSED","duration_ms":1}`, "N04": map[string]any{"resolvable": true, "addrs": []string{"127.0.0.2"}}, "N06": `[{"dev":"eth1"}]`,
		"R01": "LoadState=not-found\nActiveState=inactive\nUnitFileState=\n", "R02": "1 other\n", "R03": "1.00 3.00 4.00 1/100 42\n", "R04": "Max open files 10 4096 files\n", "R05": timeSyncObservationFixture("no"),
		"A01": `{"exists":false,"lookup_status":"NOTFOUND","source":"libc NSS"}`, "A02": `{"exists":false,"lookup_status":"NOTFOUND","source":"libc NSS"}`, "A03": "permitrootlogin yes\n", "A04": "Permissive",
		"D01": "installed=false\n", "D02": "exists=false\n", "D03": runtimeObservationFixture("Python 3.10.0\n"), "D05": "DATA_PATH=/other\n", "L01": "59.00 123.45\n",
	}
	if id == "L02" {
		return positive[id].(string) + `{"__REALTIME_TIMESTAMP":"7000000000","MESSAGE":"Killed process 42 (worker)"}` + "\n" + journalAuditObservationFixture
	}
	if id == "L03" {
		return positive[id].(string) + `{"__REALTIME_TIMESTAMP":"7000000000","MESSAGE":"Scheduled restart job"}` + "\n" + journalAuditObservationFixture
	}
	if id == "S04" || id == "S05" {
		return filesystemObservationFixture(negativeValues[id].(string))
	}
	return negativeValues[id]
}

func fixtureReport(a compiler.Artifact, id string, negative bool) []byte {
	observations := []map[string]any{}
	seen := map[string]bool{}
	for _, origin := range a.Origins {
		key := origin.Type + "|" + origin.Resource + "|" + origin.Property
		if seen[key] {
			continue
		}
		seen[key] = true
		actual := observationFixture(id, negative)
		success := true
		if fields, ok := actual.(map[string]any); ok {
			actual = fields[origin.Property]
		}
		if origin.Evaluator == "N04_RCODE" {
			actual = `{"rcode":0,"truncated":false,"timed_out":false,"duration_ms":1}`
		}
		if origin.Collection {
			actual = float64(0)
		} else if negative && origin.Evaluator == "" {
			success = false
			if origin.MissingIsError && actual == true {
				success = true
			}
		}
		observations = append(observations, map[string]any{"resource-id": origin.Resource, "resource-type": origin.Type, "property": origin.Property, "successful": success, "err": nil, "matcher-result": map[string]any{"actual": actual}})
	}
	raw, _ := json.Marshal(map[string]any{"results": observations})
	return raw
}

func TestAllTemplatesBehaviorContracts(t *testing.T) {
	for _, definition := range template.List() {
		if definition["version"] != "1.0.0" {
			continue
		}
		id := definition["template_id"].(string)
		t.Run(id, func(t *testing.T) {
			p := catalogPlan(t, id)
			input := compiler.HostInput{Roles: []string{"app"}}
			t.Run("PASS_and_boundary", func(t *testing.T) {
				a := selectedArtifact(t, p, input)
				results, err := compiler.Normalize(a, fixtureReport(a, id, false))
				if err != nil || len(results) != 1 || results[0].Status != "PASS" {
					t.Fatalf("%+v %v", results, err)
				}
			})
			t.Run("FAIL", func(t *testing.T) {
				a := selectedArtifact(t, p, input)
				results, err := compiler.Normalize(a, fixtureReport(a, id, true))
				if err != nil || len(results) != 1 || results[0].Status != "FAIL" {
					t.Fatalf("%+v %v", results, err)
				}
			})
			t.Run("artifact_roundtrip", func(t *testing.T) {
				a := selectedArtifact(t, p, input)
				raw, err := json.Marshal(a)
				if err != nil {
					t.Fatal(err)
				}
				var restored compiler.Artifact
				if err := json.Unmarshal(raw, &restored); err != nil {
					t.Fatal(err)
				}
				for _, negative := range []bool{false, true} {
					expected := "PASS"
					if negative {
						expected = "FAIL"
					}
					results, err := compiler.Normalize(restored, fixtureReport(restored, id, negative))
					if err != nil || len(results) != 1 || results[0].Status != expected {
						t.Fatalf("expected=%s got=%+v err=%v", expected, results, err)
					}
				}
			})
			t.Run("missing_evidence_ERROR", func(t *testing.T) {
				a := selectedArtifact(t, p, input)
				results, err := compiler.Normalize(a, []byte(`{"results":[]}`))
				if err != nil || len(results) != 1 || results[0].Status != "ERROR" {
					t.Fatalf("%+v %v", results, err)
				}
			})
			t.Run("field_override", func(t *testing.T) {
				item := &p.Roles[0].Items[0]
				keys := []string{}
				for key := range item.Params {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				key := keys[0]
				item.OverridableParams = []string{key}
				override := input
				override.HostOverrides = map[string]map[string]any{"sample": {key: item.Params[key]}}
				a := selectedArtifact(t, p, override)
				if a.ParameterSources["sample"][key] != "host" {
					t.Fatal("host origin missing")
				}
				item.OverridableParams = []string{}
			})
			t.Run("independent_property_mapping", func(t *testing.T) {
				copy := p.Roles[0].Items[0]
				copy.ItemID = "second"
				p.Roles[0].Items = append(p.Roles[0].Items, copy)
				defer func() { p.Roles[0].Items = p.Roles[0].Items[:1] }()
				a := selectedArtifact(t, p, input)
				results, err := compiler.Normalize(a, fixtureReport(a, id, false))
				if err != nil || len(results) != 2 || results[0].Status != "PASS" || results[1].Status != "PASS" || results[0].ItemID == results[1].ItemID {
					t.Fatalf("%+v %v", results, err)
				}
			})
			t.Run("disabled_SKIP", func(t *testing.T) {
				p.Roles[0].Items[0].Enabled = false
				defer func() { p.Roles[0].Items[0].Enabled = true }()
				a := selectedArtifact(t, p, input)
				results, err := compiler.Normalize(a, []byte(`{"results":[]}`))
				if err != nil || len(a.Origins) != 0 || len(results) != 1 || results[0].Status != "SKIP" {
					t.Fatalf("%+v %v", results, err)
				}
			})
			t.Run("unproved_capability_rejected", func(t *testing.T) {
				missing := input
				missing.Capabilities = map[string]bool{}
				if _, err := compiler.CompileExecutionModes(p, missing); err == nil {
					t.Fatal("unknown capabilities accepted")
				}
			})
		})
	}
}

func TestPlanPreviewDoesNotAssumeRolesAreColocated(t *testing.T) {
	p := catalogPlan(t, "S01")
	other := p.Roles[0]
	other.RoleID = "db"
	other.Items = append([]plan.CheckItem{}, other.Items...)
	other.Items[0].ItemID = "absent"
	other.Items[0].Params = map[string]any{"path": "/data", "exists": false}
	p.Roles = append(p.Roles, other)
	preview, err := compiler.CompilePlan(p)
	if err != nil || len(preview.Roles) != 2 {
		t.Fatalf("separate roles: %+v %v", preview, err)
	}
	if _, err := compiler.CompileExecutionModes(p, compiler.HostInput{Roles: []string{"app", "db"}}); err == nil || !strings.Contains(err.Error(), "RESOURCE_CONFLICT") {
		t.Fatalf("colocated conflict was not rejected: %v", err)
	}
}

func textObservationReport(a compiler.Artifact, text string) []byte {
	for _, origin := range a.Origins {
		if (origin.Evaluator == "L02" || origin.Evaluator == "L03") && !strings.Contains(text, "__RS_AUDIT_OLDER__\n") {
			text += journalAuditObservationFixture
			break
		}
	}
	for _, origin := range a.Origins {
		if (origin.Evaluator == "S04" || origin.Evaluator == "S05") && !strings.Contains(text, "__RS_MOUNTINFO__\n") {
			text = filesystemObservationFixture(text)
			break
		}
	}
	observations := []map[string]any{}
	for _, origin := range a.Origins {
		actual := any(text)
		if origin.Collection {
			actual = float64(0)
		}
		observations = append(observations, map[string]any{"resource-id": origin.Resource, "resource-type": origin.Type, "property": origin.Property, "successful": true, "err": nil, "matcher-result": map[string]any{"actual": actual}})
	}
	raw, _ := json.Marshal(map[string]any{"results": observations})
	return raw
}

func TestInodePercentageUsesExactCountsAndRejectsUnknownTotal(t *testing.T) {
	p := catalogPlan(t, "S05")
	p.Roles[0].Items[0].Params = map[string]any{"path": "/data", "min_percent": float64(50)}
	a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
	for _, tt := range []struct{ actual, status string }{{"4096\n1000\n500\n400\n10\n5\nxfs\n/data\n", "PASS"}, {"4096\n1000\n500\n400\n10\n4\nxfs\n/data\n", "FAIL"}, {"4096\n1000\n500\n400\n0\n0\nxfs\n/data\n", "ERROR"}} {
		results, err := compiler.Normalize(a, textObservationReport(a, tt.actual))
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("%+v %v", results, err)
		}
	}
}

func TestSystemdLimitVariantRemainsConfigurationEvidence(t *testing.T) {
	doc, _ := template.Lookup("R04", "1.0.0-config")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "limit", Enabled: true, TemplateID: "R04", TemplateVersion: "1.0.0-config", TemplateDigest: snapshot.Digest, Params: map[string]any{"unit": "app.service", "limit": "NOFILE", "minimum": float64(1024)}}
	a, err := compiler.Compile(&plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{item}}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Items[0].EvidenceMode != "CONFIG" {
		t.Fatal("configured limit was presented as effective process evidence")
	}
	for _, tt := range []struct{ actual, status string }{{"1024\n", "PASS"}, {"1023\n", "FAIL"}, {"infinity\n", "PASS"}, {"invalid\n", "ERROR"}} {
		results, err := compiler.Normalize(a, textObservationReport(a, tt.actual))
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("%+v %v", results, err)
		}
	}
}

func TestOOMMarkersCannotBeDeduplicatedByTimeAlone(t *testing.T) {
	p := catalogPlan(t, "L02")
	p.Roles[0].Items[0].Params["max_events"] = float64(1)
	a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
	prefix := strings.TrimSuffix(observationFixture("L02", false).(string), journalAuditObservationFixture)
	for _, tt := range []struct{ pid, status string }{{"43", "ERROR"}, {"42", "PASS"}} {
		actual := prefix + `{"__REALTIME_TIMESTAMP":"7000000000","MESSAGE":"oom-kill: task=worker,pid=` + tt.pid + `"}` + "\n" + `{"__REALTIME_TIMESTAMP":"7000000001","MESSAGE":"Out of memory: Killed process 42 (worker)"}` + "\n"
		results, err := compiler.Normalize(a, textObservationReport(a, actual))
		if err != nil || len(results) != 1 || results[0].Status != tt.status {
			t.Fatalf("pid=%s: %+v %v", tt.pid, results, err)
		}
	}
}
