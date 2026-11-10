package webui

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
)

func (s *Server) previewRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source         string `json:"source"`
		NormalizeDraft bool   `json:"normalize_draft"`
		Inventory      struct {
			Source string `json:"source"`
			Format string `json:"format"`
		} `json:"inventory"`
		RoleMap        map[string][]string `json:"role_map"`
		Overrides      []preview.Override  `json:"field_overrides"`
		PartialHostIDs []string            `json:"partial_host_ids,omitempty"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	validated, err := validateSource(planRequest{Source: body.Source, NormalizeDraft: body.NormalizeDraft})
	if err != nil {
		writeValidationError(w, err)
		return
	}
	raw, err := json.Marshal(validated.Document)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "PLAN_ERROR: invalid document")
		return
	}
	p, err := plan.Load(bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	format := body.Inventory.Format
	if format == "" {
		format = "quick"
	}
	hosts, err := inventory.Parse(body.Inventory.Source, format)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if len(body.PartialHostIDs) > 0 {
		selected := make(map[string]bool, len(body.PartialHostIDs))
		for _, id := range body.PartialHostIDs {
			selected[id] = true
		}
		hosts, body.RoleMap, body.Overrides = filterPartialInputs(hosts, body.RoleMap, body.Overrides, selected)
		if len(hosts) != len(selected) {
			writeError(w, http.StatusUnprocessableEntity, "PARTIAL_HOST_NOT_IN_INVENTORY")
			return
		}
	}
	// Static preview validates bindings and compilation. Role counts, colocation,
	// architecture, and evidence coverage remain execution topology results so a
	// legitimate PartialRun is not rejected as a plan error.
	previewPlan := *p
	previewPlan.Roles = append([]plan.Role(nil), p.Roles...)
	for i := range previewPlan.Roles {
		previewPlan.Roles[i].MinNodes = 0
		previewPlan.Roles[i].MaxNodes = nil
		previewPlan.Roles[i].AllowColocation = true
	}
	result, err := preview.Build(&previewPlan, preview.Input{Hosts: hosts, RoleMap: body.RoleMap, Overrides: body.Overrides})
	if err != nil {
		writeValidationError(w, err)
		return
	}
	var response map[string]any
	rawResult, err := json.Marshal(result)
	if err != nil || json.Unmarshal(rawResult, &response) != nil {
		writeError(w, http.StatusInternalServerError, "PREVIEW_RESPONSE_ERROR")
		return
	}
	response["validated_plan"] = validated.Document
	response["plan_digest"] = validated.Digest
	writeJSON(w, http.StatusOK, response)
}
