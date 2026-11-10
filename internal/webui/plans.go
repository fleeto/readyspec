package webui

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/planstore"
	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/shellassert"
)

const draftIndex = "plans/drafts/web-index.json"

type planRequest struct {
	Source           string              `json:"source"`
	NormalizeDraft   bool                `json:"normalize_draft"`
	DraftKey         string              `json:"draft_key,omitempty"`
	ExpectedRevision string              `json:"expected_revision,omitempty"`
	Host             *compiler.HostInput `json:"host,omitempty"`
}
type validation struct {
	Document map[string]any `json:"document"`
	Digest   string         `json:"digest"`
}
type savedPlan struct {
	Key       string         `json:"key"`
	Document  map[string]any `json:"document"`
	UpdatedAt string         `json:"updated_at"`
	Revision  string         `json:"revision"`
	Kind      string         `json:"kind,omitempty"`
}

// Normalization is explicit and only applies to authored DRAFT documents.
// Strict imports retain declared digests so tampering cannot be silently signed.
func validateSource(body planRequest) (*validation, error) {
	doc, err := safeyaml.Parse(strings.NewReader(body.Source))
	if err != nil {
		return nil, err
	}
	if body.NormalizeDraft && doc["state"] == "DRAFT" {
		delete(doc, "digest")
		if templates, ok := doc["embedded_templates"].([]any); ok {
			lock := make(map[string]any, len(templates))
			for _, entry := range templates {
				template, ok := entry.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("PLAN_ERROR: embedded template must be an object")
				}
				id, idOK := template["template_id"].(string)
				version, versionOK := template["version"].(string)
				if !idOK || !versionOK {
					return nil, fmt.Errorf("PLAN_ERROR: missing template identity")
				}
				digest, err := semantic.TemplateDigest(template)
				if err != nil {
					return nil, fmt.Errorf("PLAN_ERROR: cannot calculate template digest")
				}
				template["digest"] = digest
				lock[id+"@"+version] = digest
			}
			doc["template_lock"] = lock
			if roles, ok := doc["roles"].([]any); ok {
				for _, entry := range roles {
					role, ok := entry.(map[string]any)
					if !ok {
						continue
					}
					items, _ := role["items"].([]any)
					for _, entry := range items {
						item, ok := entry.(map[string]any)
						if !ok {
							continue
						}
						id, _ := item["template_id"].(string)
						version, _ := item["template_version"].(string)
						if digest, ok := lock[id+"@"+version]; ok {
							item["template_digest"] = digest
						}
					}
				}
			}
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: invalid document")
	}
	p, err := plan.Load(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	doc["digest"] = p.Digest
	return &validation{Document: doc, Digest: p.Digest}, nil
}

func (s *Server) validatePlan(w http.ResponseWriter, r *http.Request) {
	var body planRequest
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	result, err := validateSource(body)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) compilePlan(w http.ResponseWriter, r *http.Request) {
	var body planRequest
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	validated, err := validateSource(body)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	raw, err := json.Marshal(validated.Document)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "PLAN_ERROR")
		return
	}
	p, err := plan.Load(bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var artifact any
	if body.Host != nil {
		artifact, err = compiler.CompileExecutionModes(p, *body.Host)
	} else {
		artifact, err = compiler.CompilePlan(p)
	}
	if err != nil {
		var diagnostic *compiler.Diagnostic
		if errors.As(err, &diagnostic) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "diagnostics": []*compiler.Diagnostic{diagnostic}})
		} else {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, artifact)
}

func (s *Server) readPlans() (map[string]savedPlan, error) {
	index := make(map[string]savedPlan)
	if err := s.options.Store.ReadJSON(draftIndex, &index); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if index == nil {
		return nil, fmt.Errorf("invalid draft index")
	}
	for key, entry := range index {
		raw, err := json.Marshal(entry.Document)
		if err != nil {
			return nil, err
		}
		_, err = plan.LoadDraft(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if entry.Key != key || key == "" {
			return nil, fmt.Errorf("invalid draft identity")
		}
		if _, err := time.Parse(time.RFC3339, entry.UpdatedAt); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	index, err := s.readPlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取工作区草稿，请检查工作区文件。")
		return
	}
	items := make([]savedPlan, 0, len(index))
	consumed := make(map[string]bool)
	records, err := s.plans.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取工作区方案记录。")
		return
	}
	for _, record := range records {
		if record.SourceDraftKey != "" {
			consumed[record.SourceDraftKey] = true
		}
		doc := record.ReleaseSnapshot
		if record.ArchiveSnapshot != nil {
			doc = record.ArchiveSnapshot
		}
		items = append(items, savedPlan{Key: record.RecordKey, Document: doc, UpdatedAt: record.UpdatedAt, Revision: record.Revision, Kind: record.Kind})
	}
	for _, entry := range index {
		if consumed[entry.Key] {
			continue
		}
		revision, revisionErr := draftRevision(entry.Document)
		if revisionErr != nil {
			writeError(w, http.StatusInternalServerError, "无法计算草稿修订标识。")
			return
		}
		items = append(items, savedPlan{Key: entry.Key, Document: entry.Document, UpdatedAt: entry.UpdatedAt, Revision: revision, Kind: "draft"})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].Key < items[j].Key
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	if record, err := s.plans.Get(key); err == nil {
		document := record.ReleaseSnapshot
		if record.ArchiveSnapshot != nil {
			document = record.ArchiveSnapshot
		}
		writeJSON(w, http.StatusOK, savedPlan{Key: record.RecordKey, Document: document, UpdatedAt: record.UpdatedAt, Revision: record.Revision, Kind: record.Kind})
		return
	} else if !errors.Is(err, planstore.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	index, err := s.readPlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	entry, ok := index[key]
	if !ok {
		writeError(w, http.StatusNotFound, "PLAN_RECORD_NOT_FOUND")
		return
	}
	revision, err := draftRevision(entry.Document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	entry.Revision, entry.Kind = revision, "draft"
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) savePlan(w http.ResponseWriter, r *http.Request) {
	var body planRequest
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	doc, err := plan.LoadDraft(strings.NewReader(body.Source))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if doc["state"] != "DRAFT" {
		writeError(w, http.StatusConflict, "仅允许保存草稿，已发布或已停用的方案不可覆盖。")
		return
	}
	// Saving is not validation. Never persist a stale claim about the digest.
	delete(doc, "digest")
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	index, err := s.readPlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取工作区草稿，请检查工作区文件。")
		return
	}
	key := body.DraftKey
	if key != "" {
		entry, exists := index[key]
		if !exists {
			writeError(w, http.StatusConflict, "草稿记录不存在，请重新打开或另存为新草稿。")
			return
		}
		if body.ExpectedRevision == "" {
			writeError(w, http.StatusConflict, "草稿缺少修订标识，请重新打开后再保存。")
			return
		}
		currentRevision, revisionErr := draftRevision(entry.Document)
		if revisionErr != nil || body.ExpectedRevision != currentRevision {
			writeError(w, http.StatusConflict, "草稿已在其他位置修改，请重新打开后再保存。")
			return
		}
		records, recordsErr := s.plans.List()
		if recordsErr != nil {
			writeError(w, http.StatusInternalServerError, "无法确认方案生命周期状态。")
			return
		}
		for _, record := range records {
			if record.SourceDraftKey == key {
				writeError(w, http.StatusConflict, "此草稿已发布并冻结，请创建新版本草稿。")
				return
			}
		}
	} else {
		key = "draft-" + rand.Text()
		if _, exists := index[key]; exists {
			writeError(w, http.StatusConflict, "同标识草稿已存在，请打开原记录后编辑。")
			return
		}
	}
	updated := time.Now().UTC().Format(time.RFC3339)
	doc["updated_at"] = updated
	index[key] = savedPlan{Key: key, Document: doc, UpdatedAt: updated}
	if err := s.options.Store.WriteJSON(draftIndex, index); err != nil {
		writeError(w, http.StatusInternalServerError, "未能确认草稿保存完成，请检查工作区后重试。")
		return
	}
	revision, err := draftRevision(doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法计算草稿修订标识。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": doc, "draft_key": key, "revision": revision, "validated": false})
}

func draftRevision(document map[string]any) (string, error) {
	copy := make(map[string]any, len(document))
	for key, value := range document {
		if key != "updated_at" && key != "digest" {
			copy[key] = value
		}
	}
	return semantic.Digest(copy)
}

func (s *Server) deleteDraft(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	index, err := s.readPlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	key := r.PathValue("key")
	entry, ok := index[key]
	if !ok {
		writeError(w, http.StatusNotFound, "PLAN_RECORD_NOT_FOUND")
		return
	}
	revision, err := draftRevision(entry.Document)
	if err != nil || body.ExpectedRevision == "" || revision != body.ExpectedRevision {
		writeError(w, http.StatusConflict, "PLAN_REVISION_CONFLICT")
		return
	}
	records, err := s.plans.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	for _, record := range records {
		if record.SourceDraftKey == key {
			writeError(w, http.StatusConflict, "PLAN_DRAFT_FROZEN")
			return
		}
	}
	delete(index, key)
	if err := s.options.Store.WriteJSON(draftIndex, index); err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) releasePlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	index, err := s.readPlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	}
	key := r.PathValue("key")
	entry, ok := index[key]
	if !ok {
		writeError(w, http.StatusNotFound, "PLAN_RECORD_NOT_FOUND")
		return
	}
	revision, err := draftRevision(entry.Document)
	if err != nil || body.ExpectedRevision == "" || revision != body.ExpectedRevision {
		writeError(w, http.StatusConflict, "PLAN_REVISION_CONFLICT")
		return
	}
	if records, listErr := s.plans.List(); listErr != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_STORE_ERROR")
		return
	} else {
		for _, record := range records {
			if record.SourceDraftKey == key {
				if record.SourceRevision != body.ExpectedRevision {
					writeError(w, http.StatusConflict, "PLAN_REVISION_CONFLICT")
					return
				}
				writeJSON(w, http.StatusOK, record)
				return
			}
		}
	}
	record, err := s.plans.Release(entry.Document, key, revision)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, planstore.ErrConflict) || errors.Is(err, planstore.ErrRevision) {
			status = http.StatusConflict
		}
		writeError(w, status, safePlanError(err))
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (s *Server) archivePlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	record, err := s.plans.Archive(r.PathValue("key"), body.ExpectedRevision)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, planstore.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, safePlanError(err))
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) importPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	doc, err := safeyaml.Parse(strings.NewReader(body.Source))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, safePlanError(err))
		return
	}
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	record, created, err := s.plans.Import(doc)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, planstore.ErrConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, safePlanError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"record": record, "created": created})
}

func (s *Server) exportPlan(w http.ResponseWriter, r *http.Request) {
	record, err := s.plans.Get(r.PathValue("key"))
	if err != nil {
		writeError(w, http.StatusNotFound, "PLAN_RECORD_NOT_FOUND")
		return
	}
	document := record.ReleaseSnapshot
	if record.ArchiveSnapshot != nil {
		document = record.ArchiveSnapshot
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PLAN_EXPORT_ERROR")
		return
	}
	filename := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, record.PlanID+"-"+record.Version+".yaml")
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+path.Base(filename)+"\"")
	_, _ = w.Write(raw)
}

func safePlanError(err error) string {
	for _, marker := range []string{"PLAN_", "DRAFT_", "RELEASE_MANIFEST_", "TEMPLATE_", "IMPLEMENTATION_", "PARAM_"} {
		if at := strings.Index(err.Error(), marker); at >= 0 {
			end := at
			for end < len(err.Error()) && (err.Error()[end] >= 'A' && err.Error()[end] <= 'Z' || err.Error()[end] >= '0' && err.Error()[end] <= '9' || err.Error()[end] == '_') {
				end++
			}
			return err.Error()[at:end]
		}
	}
	return "PLAN_OPERATION_REJECTED"
}

func (s *Server) promoteTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Template map[string]any `json:"template"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	if body.Template == nil {
		writeError(w, http.StatusBadRequest, "TEMPLATE_REQUIRED")
		return
	}
	s.plansMu.Lock()
	defer s.plansMu.Unlock()
	item, err := s.plans.PromoteTemplate(body.Template)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, planstore.ErrConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, safePlanError(err))
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) shellSelfTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Assertions           []map[string]any        `json:"assertions"`
		ExpectExitCode       int                     `json:"expect_exit_code"`
		Observation          shellassert.Observation `json:"observation"`
		SimulateStartFailure bool                    `json:"simulate_start_failure,omitempty"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	assertions, err := shellassert.BuildAssertions(toAnyAssertions(body.Assertions), body.ExpectExitCode)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "CUSTOM_ASSERTION_INVALID")
		return
	}
	if body.SimulateStartFailure {
		body.Observation.ExecutionError = "受控命令未能启动"
	}
	result := shellassert.Evaluate(assertions, body.Observation)
	writeJSON(w, http.StatusOK, result)
}

func toAnyAssertions(assertions []map[string]any) []any {
	values := make([]any, len(assertions))
	for i := range assertions {
		values[i] = assertions[i]
	}
	return values
}
