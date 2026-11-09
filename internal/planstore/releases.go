// Package planstore persists immutable released plan snapshots.
package planstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/fleeto/readyspec/internal/buildinfo"
	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/template"
	"github.com/fleeto/readyspec/internal/workspace"
)

const (
	ManifestSchema    = "readyspec.release-manifest/v1"
	CompilerVersion   = buildinfo.Version
	releaseDir        = "plans/releases"
	publicTemplateDir = "templates/public"
)

var (
	ErrNotFound = errors.New("PLAN_RECORD_NOT_FOUND")
	ErrConflict = errors.New("PLAN_VERSION_CONFLICT")
	ErrRevision = errors.New("PLAN_REVISION_CONFLICT")
)

type Record struct {
	SchemaVersion      string         `json:"schema_version"`
	Kind               string         `json:"kind"` // released or archive_only
	RecordKey          string         `json:"record_key"`
	PlanID             string         `json:"plan_id"`
	Version            string         `json:"version"`
	Revision           string         `json:"revision"`
	ReleaseSnapshot    map[string]any `json:"release_snapshot"`
	ArchiveSnapshot    map[string]any `json:"archive_snapshot,omitempty"`
	ReleaseFingerprint string         `json:"release_fingerprint"`
	SourceDraftKey     string         `json:"source_draft_key,omitempty"`
	SourceRevision     string         `json:"source_revision,omitempty"`
	UpdatedAt          string         `json:"updated_at"`
}

type Store struct{ workspace *workspace.Store }

type PublicTemplate struct {
	SchemaVersion string         `json:"schema_version"`
	Key           string         `json:"key"`
	TemplateID    string         `json:"template_id"`
	Version       string         `json:"version"`
	Digest        string         `json:"digest"`
	Snapshot      map[string]any `json:"snapshot"`
	Revision      string         `json:"revision"`
	UpdatedAt     string         `json:"updated_at"`
}

func New(ws *workspace.Store) (*Store, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	return &Store{workspace: ws}, nil
}

func (s *Store) List() ([]Record, error) {
	entries, err := s.workspace.ReadDir(releaseDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".json" {
			continue
		}
		var record Record
		if err := s.workspace.ReadJSON(path.Join(releaseDir, entry.Name()), &record); err != nil {
			return nil, fmt.Errorf("read plan lifecycle record: %w", err)
		}
		if err := s.validateRecord(record); err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].RecordKey < items[j].RecordKey
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items, nil
}

func (s *Store) Find(planID, version string) (*Record, error) {
	items, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].PlanID == planID && items[i].Version == version {
			return &items[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) Get(recordKey string) (*Record, error) {
	items, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].RecordKey == recordKey {
			return &items[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) PublicTemplates() ([]PublicTemplate, error) {
	entries, err := s.workspace.ReadDir(publicTemplateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []PublicTemplate{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]PublicTemplate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".json" {
			continue
		}
		var item PublicTemplate
		if err := s.workspace.ReadJSON(path.Join(publicTemplateDir, entry.Name()), &item); err != nil {
			return nil, fmt.Errorf("PUBLIC_TEMPLATE_CORRUPT")
		}
		if item.SchemaVersion != "readyspec.public-template/v1" || item.Key != key(item.TemplateID, item.Version) || entry.Name() != item.Key+".json" || item.Snapshot == nil || item.Snapshot["template_id"] != item.TemplateID || item.Snapshot["version"] != item.Version || item.Snapshot["source"] != "USER_PUBLIC" || item.Snapshot["digest"] != item.Digest {
			return nil, fmt.Errorf("PUBLIC_TEMPLATE_CORRUPT")
		}
		digest, digestErr := semantic.TemplateDigest(item.Snapshot)
		if digestErr != nil || digest != item.Digest {
			return nil, fmt.Errorf("PUBLIC_TEMPLATE_CORRUPT")
		}
		if _, err := plan.ValidateTemplate(item.Snapshot); err != nil {
			return nil, fmt.Errorf("PUBLIC_TEMPLATE_CORRUPT")
		}
		revision := item.Revision
		item.Revision = ""
		want, digestErr := semantic.Digest(item)
		item.Revision = revision
		if digestErr != nil || want != revision {
			return nil, fmt.Errorf("PUBLIC_TEMPLATE_CORRUPT")
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TemplateID == items[j].TemplateID {
			return items[i].Version < items[j].Version
		}
		return items[i].TemplateID < items[j].TemplateID
	})
	return items, nil
}

func (s *Store) PromoteTemplate(document map[string]any) (*PublicTemplate, error) {
	copy, err := clone(document)
	if err != nil {
		return nil, err
	}
	if copy["source"] != "PLAN_LOCAL" {
		return nil, fmt.Errorf("TEMPLATE_PROMOTION_SOURCE_INVALID")
	}
	copy["source"] = "USER_PUBLIC"
	delete(copy, "digest")
	digest, err := semantic.TemplateDigest(copy)
	if err != nil {
		return nil, err
	}
	copy["digest"] = digest
	if _, err := plan.ValidateTemplate(copy); err != nil {
		return nil, err
	}
	if _, err := compilePublicTemplate(copy); err != nil {
		return nil, err
	}
	id, _ := copy["template_id"].(string)
	version, _ := copy["version"].(string)
	if _, builtIn := template.Lookup(id, version); builtIn {
		return nil, fmt.Errorf("TEMPLATE_BUILTIN_IMMUTABLE")
	}
	items, err := s.PublicTemplates()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.TemplateID != id || item.Version != version {
			continue
		}
		if item.Digest == digest {
			return &item, nil
		}
		return nil, ErrConflict
	}
	item := PublicTemplate{SchemaVersion: "readyspec.public-template/v1", Key: key(id, version), TemplateID: id, Version: version, Digest: digest, Snapshot: copy, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	item.Revision, err = semantic.Digest(item)
	if err != nil {
		return nil, err
	}
	if err := s.workspace.WriteJSON(path.Join(publicTemplateDir, item.Key+".json"), item); err != nil {
		return nil, err
	}
	return &item, nil
}

func compilePublicTemplate(snapshot map[string]any) (compiler.PlanArtifacts, error) {
	// CompilePlan exercises the same trusted snapshot resolver used by plan
	// compilation. A minimal one-item plan makes promotion fail closed when the
	// template's implementation cannot be compiled.
	id, _ := snapshot["template_id"].(string)
	version, _ := snapshot["version"].(string)
	digest, _ := snapshot["digest"].(string)
	params := map[string]any{}
	if schema, ok := snapshot["params_schema"].(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, rawName := range required {
			name, ok := rawName.(string)
			if !ok {
				continue
			}
			definition, _ := properties[name].(map[string]any)
			value, exists := definition["default"]
			if !exists {
				if values, ok := definition["enum"].([]any); ok && len(values) > 0 {
					value, exists = values[0], true
				}
			}
			if !exists {
				switch definition["type"] {
				case "integer", "number":
					value = 1
				case "boolean":
					value = true
				case "array":
					value = []any{}
				case "object":
					value = map[string]any{}
				default:
					value = "/tmp/readyspec"
				}
			}
			params[name] = value
		}
	}
	p := &plan.Plan{
		SchemaVersion: "readyspec.plan/v1", PlanID: "template-promotion", Name: "Template promotion",
		SoftwareName: "template", SoftwareVersion: "1", Version: "1", State: "DRAFT",
		TopologyPolicy:    plan.TopologyPolicy{AllowedArch: []string{"x86_64"}},
		EmbeddedTemplates: []plan.CheckTemplate{mustTemplate(snapshot)},
		TemplateLock:      map[string]string{id + "@" + version: digest},
		Roles:             []plan.Role{{RoleID: "test", Name: "test", MinNodes: 1, AllowColocation: true, Items: []plan.CheckItem{{ItemID: "test", Name: "test", TemplateID: id, TemplateVersion: version, TemplateDigest: digest, Importance: "REQUIRED", Enabled: true, Params: params}}}},
	}
	return compiler.CompilePlan(p)
}

func mustTemplate(snapshot map[string]any) plan.CheckTemplate {
	raw := mustJSON(snapshot)
	var result plan.CheckTemplate
	if err := json.Unmarshal(raw, &result); err != nil {
		panic("validated template failed to decode")
	}
	return result
}

func (s *Store) Release(source map[string]any, draftKey, sourceRevision string) (*Record, error) {
	draft, err := clone(source)
	if err != nil {
		return nil, err
	}
	if draft["state"] != "DRAFT" {
		return nil, fmt.Errorf("PLAN_STATE_INVALID")
	}
	if err := computePlanLocalTemplateIdentities(draft); err != nil {
		return nil, err
	}
	p, err := strictCompile(draft)
	if err != nil {
		return nil, err
	}
	if previous, err := s.Find(p.PlanID, p.Version); err == nil {
		if previous.SourceDraftKey == draftKey && previous.SourceRevision == sourceRevision {
			return previous, nil
		}
		return nil, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	previousVersions, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, previous := range previousVersions {
		if previous.PlanID != p.PlanID {
			continue
		}
		comparison, valid := plan.CompareNumericVersions(p.Version, previous.Version)
		if !valid || comparison <= 0 {
			return nil, fmt.Errorf("PLAN_VERSION_NOT_INCREASING")
		}
	}

	released := cloneMust(draft)
	released["state"] = "RELEASED"
	delete(released, "digest")
	digest, err := semantic.PlanDigest(released)
	if err != nil {
		return nil, err
	}
	released["digest"] = digest
	released["release_manifest"] = map[string]any{
		"schema_version":       ManifestSchema,
		"plan_id":              p.PlanID,
		"version":              p.Version,
		"released_plan_digest": digest,
		"template_lock":        p.TemplateLock,
		"goss_version":         goss.Version,
		"compiler_version":     CompilerVersion,
	}
	if _, err := strictCompile(released); err != nil {
		return nil, err
	}
	return s.create(Record{
		Kind: "released", PlanID: p.PlanID, Version: p.Version,
		ReleaseSnapshot: released, ReleaseFingerprint: digest,
		SourceDraftKey: draftKey, SourceRevision: sourceRevision,
	})
}

func (s *Store) Archive(recordKey, expectedRevision string) (*Record, error) {
	record, err := s.Get(recordKey)
	if err != nil {
		return nil, err
	}
	if expectedRevision == "" || record.Revision != expectedRevision {
		return nil, ErrRevision
	}
	if record.ArchiveSnapshot != nil {
		return record, nil
	}
	archived := cloneMust(record.ReleaseSnapshot)
	archived["state"] = "ARCHIVED"
	delete(archived, "digest")
	digest, err := semantic.PlanDigest(archived)
	if err != nil {
		return nil, err
	}
	archived["digest"] = digest
	if _, err := strictCompile(archived); err != nil {
		return nil, err
	}
	record.ArchiveSnapshot = archived
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.write(*record)
}

func (s *Store) Import(document map[string]any) (*Record, bool, error) {
	doc := cloneMust(document)
	if doc["state"] == "DRAFT" {
		return nil, false, fmt.Errorf("PLAN_IMPORT_STATE_INVALID")
	}
	if err := VerifyManifest(doc); err != nil {
		return nil, false, err
	}
	p, err := plan.Load(bytes.NewReader(mustJSON(doc)))
	if err != nil {
		return nil, false, err
	}
	if _, err := strictCompile(doc); err != nil {
		return nil, false, err
	}
	if existing, err := s.Find(p.PlanID, p.Version); err == nil {
		if p.State == "RELEASED" {
			if existing.Kind == "archive_only" && existing.ReleaseFingerprint == p.Digest {
				return existing, false, nil
			}
			if existing.ReleaseFingerprint == p.Digest && existing.ReleaseSnapshot != nil {
				return existing, false, nil
			}
			return nil, false, ErrConflict
		}
		if existing.ReleaseFingerprint == manifestDigest(doc) {
			if existing.ArchiveSnapshot != nil {
				return existing, false, nil
			}
			if existing.Kind != "released" || existing.ReleaseSnapshot == nil {
				return nil, false, ErrConflict
			}
			existing.ArchiveSnapshot = doc
			existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			record, err := s.write(*existing)
			return record, err == nil, err
		}
		return nil, false, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	if p.State == "RELEASED" {
		record, err := s.create(Record{Kind: "released", PlanID: p.PlanID, Version: p.Version, ReleaseSnapshot: doc, ReleaseFingerprint: p.Digest})
		return record, err == nil, err
	}
	releaseFingerprint := manifestDigest(doc)
	record, err := s.create(Record{Kind: "archive_only", PlanID: p.PlanID, Version: p.Version, ArchiveSnapshot: doc, ReleaseFingerprint: releaseFingerprint})
	return record, err == nil, err
}

func VerifyManifest(document map[string]any) error {
	manifest, ok := document["release_manifest"].(map[string]any)
	if !ok || manifest["schema_version"] != ManifestSchema {
		return fmt.Errorf("RELEASE_MANIFEST_UNVERIFIABLE")
	}
	planID, _ := document["plan_id"].(string)
	version, _ := document["version"].(string)
	digest, _ := document["digest"].(string)
	if manifest["plan_id"] != planID || manifest["version"] != version ||
		manifest["released_plan_digest"] != manifestDigest(document) ||
		manifest["goss_version"] != goss.Version || manifest["compiler_version"] != CompilerVersion {
		return fmt.Errorf("RELEASE_MANIFEST_UNVERIFIABLE")
	}
	if document["state"] == "RELEASED" && digest != manifest["released_plan_digest"] {
		return fmt.Errorf("RELEASE_MANIFEST_UNVERIFIABLE")
	}
	lockRaw, err := json.Marshal(manifest["template_lock"])
	if err != nil || len(lockRaw) == 0 || string(lockRaw) == "null" {
		return fmt.Errorf("RELEASE_MANIFEST_UNVERIFIABLE")
	}
	var lock map[string]any
	if err := json.Unmarshal(lockRaw, &lock); err != nil || lock == nil {
		return fmt.Errorf("RELEASE_MANIFEST_UNVERIFIABLE")
	}
	encoded, _ := json.Marshal(document["template_lock"])
	manifestLock, _ := json.Marshal(lock)
	if !bytes.Equal(encoded, manifestLock) {
		return fmt.Errorf("RELEASE_MANIFEST_UNVERIFIABLE")
	}
	return nil
}

func (s *Store) create(record Record) (*Record, error) {
	record.SchemaVersion = "readyspec.plan-record/v1"
	record.RecordKey = key(record.PlanID, record.Version)
	if _, err := s.Get(record.RecordKey); err == nil {
		return nil, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.write(record)
}

func (s *Store) write(record Record) (*Record, error) {
	record.Revision = ""
	revision, err := semantic.Digest(record)
	if err != nil {
		return nil, err
	}
	record.Revision = revision
	if err := s.workspace.WriteJSON(path.Join(releaseDir, record.RecordKey+".json"), record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) validateRecord(record Record) error {
	if record.SchemaVersion != "readyspec.plan-record/v1" || record.RecordKey != key(record.PlanID, record.Version) ||
		record.Kind != "released" && record.Kind != "archive_only" || record.ReleaseFingerprint == "" {
		return fmt.Errorf("PLAN_RECORD_CORRUPT")
	}
	revision := record.Revision
	record.Revision = ""
	want, err := semantic.Digest(record)
	if err != nil || revision != want {
		return fmt.Errorf("PLAN_RECORD_CORRUPT")
	}
	if record.Kind == "released" {
		if record.ReleaseSnapshot == nil || record.ReleaseSnapshot["state"] != "RELEASED" || record.ReleaseSnapshot["plan_id"] != record.PlanID || record.ReleaseSnapshot["version"] != record.Version || record.ReleaseSnapshot["digest"] != record.ReleaseFingerprint {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
		if _, err := strictCompile(record.ReleaseSnapshot); err != nil {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
		if err := VerifyManifest(record.ReleaseSnapshot); err != nil {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
	}
	if record.ArchiveSnapshot != nil {
		if record.ArchiveSnapshot["state"] != "ARCHIVED" || record.ArchiveSnapshot["plan_id"] != record.PlanID || record.ArchiveSnapshot["version"] != record.Version {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
		if _, err := strictCompile(record.ArchiveSnapshot); err != nil {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
		if err := VerifyManifest(record.ArchiveSnapshot); err != nil {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
		if manifestDigest(record.ArchiveSnapshot) != record.ReleaseFingerprint {
			return fmt.Errorf("PLAN_RECORD_CORRUPT")
		}
	}
	if record.Kind == "archive_only" && record.ArchiveSnapshot == nil {
		return fmt.Errorf("PLAN_RECORD_CORRUPT")
	}
	return nil
}

func computePlanLocalTemplateIdentities(document map[string]any) error {
	templates, ok := document["embedded_templates"].([]any)
	if !ok {
		return fmt.Errorf("PLAN_SCHEMA_ERROR: embedded_templates must be an array")
	}
	computed := map[string]string{}
	for _, raw := range templates {
		template, ok := raw.(map[string]any)
		if !ok || template["source"] != "PLAN_LOCAL" {
			continue
		}
		digest, err := semantic.TemplateDigest(template)
		if err != nil {
			return err
		}
		id, idOK := template["template_id"].(string)
		version, versionOK := template["version"].(string)
		if !idOK || !versionOK {
			return fmt.Errorf("PLAN_SCHEMA_ERROR: local template identity is invalid")
		}
		template["digest"] = digest
		computed[id+"@"+version] = digest
	}
	if len(computed) == 0 {
		return nil
	}
	lock, ok := document["template_lock"].(map[string]any)
	if !ok {
		return fmt.Errorf("PLAN_SCHEMA_ERROR: template_lock must be an object")
	}
	roles, ok := document["roles"].([]any)
	if !ok {
		return fmt.Errorf("PLAN_SCHEMA_ERROR: roles must be an array")
	}
	for key, digest := range computed {
		lock[key] = digest
	}
	for _, rawRole := range roles {
		role, _ := rawRole.(map[string]any)
		items, _ := role["items"].([]any)
		for _, rawItem := range items {
			item, _ := rawItem.(map[string]any)
			id, _ := item["template_id"].(string)
			version, _ := item["template_version"].(string)
			if digest, exists := computed[id+"@"+version]; exists {
				item["template_digest"] = digest
			}
		}
	}
	return nil
}

func strictCompile(document map[string]any) (*plan.Plan, error) {
	p, err := plan.Load(bytes.NewReader(mustJSON(document)))
	if err != nil {
		return nil, err
	}
	if _, err := compiler.CompilePlan(p); err != nil {
		return nil, err
	}
	return p, nil
}

func manifestDigest(document map[string]any) string {
	copy := cloneMust(document)
	copy["state"] = "RELEASED"
	delete(copy, "digest")
	digest, _ := semantic.PlanDigest(copy)
	return digest
}

func key(planID, version string) string {
	sum := sha256.Sum256([]byte(planID + "\x00" + version))
	return hex.EncodeToString(sum[:])
}

func clone(value map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneMust(value map[string]any) map[string]any {
	result, err := clone(value)
	if err != nil {
		panic("validated JSON map failed to clone: " + err.Error())
	}
	return result
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic("validated JSON value failed to encode: " + err.Error())
	}
	return raw
}
