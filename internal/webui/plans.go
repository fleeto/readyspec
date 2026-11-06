package webui

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/fleeto/readyspec/internal/semantic"
)

const draftIndex = "plans/drafts/web-index.json"

type planRequest struct {
	Source         string              `json:"source"`
	NormalizeDraft bool                `json:"normalize_draft"`
	DraftKey       string              `json:"draft_key,omitempty"`
	Host           *compiler.HostInput `json:"host,omitempty"`
}
type validation struct {
	Document map[string]any `json:"document"`
	Digest   string         `json:"digest"`
}
type savedPlan struct {
	Key       string         `json:"key"`
	Document  map[string]any `json:"document"`
	UpdatedAt string         `json:"updated_at"`
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
		writeError(w, http.StatusUnprocessableEntity, err.Error())
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
		writeError(w, http.StatusUnprocessableEntity, err.Error())
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
	for _, entry := range index {
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].Key < items[j].Key
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
		if _, exists := index[key]; !exists {
			writeError(w, http.StatusConflict, "草稿记录不存在，请重新打开或另存为新草稿。")
			return
		}
	} else {
		id, version := doc["plan_id"].(string), doc["version"].(string)
		if id != "" && version != "" {
			key = id + "@" + version
		} else {
			key = "draft-" + rand.Text()
		}
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
	writeJSON(w, http.StatusOK, map[string]any{"document": doc, "draft_key": key, "validated": false})
}
