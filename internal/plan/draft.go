package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// LoadDraft checks the bounded, renderable editor shape without certifying
// parameter values, topology, template trust, references or semantic digests.
// Incomplete drafts must still pass Load before export or execution.
func LoadDraft(r io.Reader) (map[string]any, error) {
	doc, err := safeyaml.Parse(r)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("DRAFT_SCHEMA_ERROR: invalid JSON value")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	schema, err := compiledDraftSchema()
	if err != nil {
		return nil, fmt.Errorf("INFRA_ERROR: compile embedded draft schema")
	}
	if err := schema.Validate(value); err != nil {
		return nil, fmt.Errorf("DRAFT_SCHEMA_ERROR: document is not a safe editable draft")
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("DRAFT_SCHEMA_ERROR: cannot read draft shape")
	}
	roles, items, templates := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for _, role := range p.Roles {
		if roles[role.RoleID] {
			return nil, fmt.Errorf("DRAFT_SCHEMA_ERROR: duplicate role_id")
		}
		roles[role.RoleID] = true
		for _, item := range role.Items {
			if items[item.ItemID] {
				return nil, fmt.Errorf("DRAFT_SCHEMA_ERROR: duplicate item_id")
			}
			items[item.ItemID] = true
		}
	}
	for _, template := range p.EmbeddedTemplates {
		key := template.TemplateID + "@" + template.Version
		if templates[key] {
			return nil, fmt.Errorf("DRAFT_SCHEMA_ERROR: duplicate template identity")
		}
		templates[key] = true
	}
	return doc, nil
}
