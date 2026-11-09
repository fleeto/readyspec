package planstore

import (
	"encoding/json"
	"testing"

	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/workspace"
)

func TestReleaseArchiveAndStrictImportLifecycle(t *testing.T) {
	store := newTestStore(t)
	draft := testDraft(t, "lifecycle-check")
	released, err := store.Release(draft, "draft-key", "source-revision")
	if err != nil {
		t.Fatal(err)
	}
	if released.ReleaseSnapshot["state"] != "RELEASED" {
		t.Fatalf("release state = %v", released.ReleaseSnapshot["state"])
	}
	if err := VerifyManifest(released.ReleaseSnapshot); err != nil {
		t.Fatalf("release manifest did not verify: %v", err)
	}
	if _, created, err := store.Import(released.ReleaseSnapshot); err != nil || created {
		t.Fatalf("same release import should be idempotent: created=%v err=%v", created, err)
	}

	archived, err := store.Archive(released.RecordKey, released.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ReleaseSnapshot["state"] != "RELEASED" || archived.ArchiveSnapshot["state"] != "ARCHIVED" {
		t.Fatalf("archive must retain both snapshots: release=%v archive=%v", archived.ReleaseSnapshot["state"], archived.ArchiveSnapshot["state"])
	}
	loaded, err := store.Get(released.RecordKey)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != archived.Revision {
		t.Fatal("stored archive revision did not persist")
	}
	if _, created, err := store.Import(archived.ArchiveSnapshot); err != nil || created {
		t.Fatalf("same archive import should be idempotent: created=%v err=%v", created, err)
	}

	changed := cloneMust(released.ReleaseSnapshot)
	changed["name"] = "Different semantic content"
	delete(changed, "digest")
	digest, err := semantic.PlanDigest(changed)
	if err != nil {
		t.Fatal(err)
	}
	changed["digest"] = digest
	manifest := changed["release_manifest"].(map[string]any)
	manifest["released_plan_digest"] = digest
	if _, _, err := store.Import(changed); err != ErrConflict {
		t.Fatalf("same identity with changed content must conflict, got %v", err)
	}
}

func TestReleaseComputesPlanLocalTemplateIdentity(t *testing.T) {
	store := newTestStore(t)
	draft := testDraft(t, "plan-local-release")
	local := testShellTemplate("shell-local", "printf ready")
	local["digest"] = ""
	draft["embedded_templates"] = []any{local}
	draft["roles"].([]any)[0].(map[string]any)["items"] = []any{map[string]any{
		"item_id": "local-check", "name": "Local check", "template_id": "shell-local", "template_version": "1.0.0",
		"template_digest": "", "importance": "REQUIRED", "enabled": true, "params": map[string]any{},
		"explicit_null_params": []any{}, "overridable_params": []any{},
	}}
	released, err := store.Release(draft, "local-draft", "local-revision")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := released.ReleaseSnapshot["embedded_templates"].([]any)[0].(map[string]any)
	digest := snapshot["digest"]
	if digest == "" || released.ReleaseSnapshot["template_lock"].(map[string]any)["shell-local@1.0.0"] != digest {
		t.Fatalf("release did not compute and lock the local template digest: %+v", released.ReleaseSnapshot)
	}
	item := released.ReleaseSnapshot["roles"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	if item["template_digest"] != digest {
		t.Fatalf("release item does not reference the computed digest: %+v", item)
	}
	if err := VerifyManifest(released.ReleaseSnapshot); err != nil {
		t.Fatal(err)
	}
}

func TestImportRejectsReleaseWithoutVerifiableManifest(t *testing.T) {
	store := newTestStore(t)
	released, err := store.Release(testDraft(t, "missing-manifest"), "draft-key", "source-revision")
	if err != nil {
		t.Fatal(err)
	}
	delete(released.ReleaseSnapshot, "release_manifest")
	if _, _, err := store.Import(released.ReleaseSnapshot); err == nil || err.Error() != "RELEASE_MANIFEST_UNVERIFIABLE" {
		t.Fatalf("unverifiable release manifest was accepted: %v", err)
	}
}

func TestPromoteTemplateIsStandardImmutableAndIdempotent(t *testing.T) {
	store := newTestStore(t)
	local := testShellTemplate("user-check", "printf ready")
	public, err := store.PromoteTemplate(local)
	if err != nil {
		t.Fatal(err)
	}
	if public.Snapshot["source"] != "USER_PUBLIC" || public.Snapshot["execution_mode"] != "STANDARD" {
		t.Fatalf("promotion changed source/permission incorrectly: %#v", public.Snapshot)
	}
	if local["source"] != "PLAN_LOCAL" {
		t.Fatal("promotion mutated the source snapshot")
	}
	duplicate, err := store.PromoteTemplate(local)
	if err != nil || duplicate.Digest != public.Digest {
		t.Fatalf("same promotion should be idempotent: duplicate=%+v err=%v", duplicate, err)
	}

	changed := testShellTemplate("user-check", "printf changed")
	if _, err := store.PromoteTemplate(changed); err != ErrConflict {
		t.Fatalf("same template version with changed content must conflict, got %v", err)
	}

	privileged := testShellTemplate("privileged-user-check", "printf ready")
	privileged["execution_mode"] = "PRIVILEGED"
	privileged["digest"], _ = semantic.TemplateDigest(privileged)
	if _, err := store.PromoteTemplate(privileged); err == nil {
		t.Fatal("promotion accepted a privileged user template")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	store, err := New(ws)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testDraft(t *testing.T, planID string) map[string]any {
	t.Helper()
	var document map[string]any
	raw := []byte(`{"schema_version":"readyspec.plan/v1","plan_id":"` + planID + `","name":"Lifecycle check","software_name":"sample","software_version":"1","version":"1.0.0","state":"DRAFT","topology_policy":{"allowed_arch":["x86_64"]},"roles":[{"role_id":"app","name":"Application","min_nodes":1,"max_nodes":null,"allow_colocation":false,"items":[]}],"embedded_templates":[],"template_lock":{}}`)
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func testShellTemplate(id, command string) map[string]any {
	document := map[string]any{
		"schema_version": "readyspec.template/v1", "template_id": id, "version": "1.0.0",
		"name": "User shell check", "source": "PLAN_LOCAL", "domain": "D",
		"execution_mode": "STANDARD", "evidence_mode": "EXISTENCE",
		"params_schema":  map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
		"implementation": map[string]any{"kind": "CUSTOM_SHELL", "command": command, "expect_exit_code": 0},
	}
	digest, _ := semantic.TemplateDigest(document)
	document["digest"] = digest
	return document
}
