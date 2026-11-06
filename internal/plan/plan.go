// Package plan validates self-contained plan files without contacting targets.
package plan

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/template"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/*.json
var schemas embed.FS

type Plan struct {
	SchemaVersion     string            `json:"schema_version"`
	PlanID            string            `json:"plan_id"`
	Name              string            `json:"name"`
	SoftwareName      string            `json:"software_name"`
	SoftwareVersion   string            `json:"software_version"`
	Version           string            `json:"version"`
	State             string            `json:"state"`
	Description       string            `json:"description,omitempty"`
	TopologyPolicy    TopologyPolicy    `json:"topology_policy"`
	Roles             []Role            `json:"roles"`
	EmbeddedTemplates []CheckTemplate   `json:"embedded_templates"`
	TemplateLock      map[string]string `json:"template_lock"`
	ReleaseManifest   map[string]any    `json:"release_manifest,omitempty"`
	Digest            string            `json:"digest"`
	UpdatedAt         string            `json:"updated_at,omitempty"`
}

type TopologyPolicy struct {
	AllowedArch []string `json:"allowed_arch"`
}

type Role struct {
	RoleID          string      `json:"role_id"`
	Name            string      `json:"name"`
	MinNodes        int         `json:"min_nodes"`
	MaxNodes        *int        `json:"max_nodes"`
	AllowColocation bool        `json:"allow_colocation"`
	Items           []CheckItem `json:"items"`
}

type CheckItem struct {
	ItemID             string         `json:"item_id"`
	Name               string         `json:"name"`
	TemplateID         string         `json:"template_id"`
	TemplateVersion    string         `json:"template_version"`
	TemplateDigest     string         `json:"template_digest"`
	Importance         string         `json:"importance"`
	Enabled            bool           `json:"enabled"`
	Params             map[string]any `json:"params"`
	ExplicitNullParams []string       `json:"explicit_null_params"`
	OverridableParams  []string       `json:"overridable_params"`
}

type CheckTemplate struct {
	SchemaVersion  string         `json:"schema_version"`
	TemplateID     string         `json:"template_id"`
	Version        string         `json:"version"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Source         string         `json:"source"`
	Domain         string         `json:"domain"`
	ExecutionMode  string         `json:"execution_mode"`
	EvidenceMode   string         `json:"evidence_mode"`
	ParamsSchema   map[string]any `json:"params_schema"`
	Implementation map[string]any `json:"implementation"`
	Digest         string         `json:"digest"`
}

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) { return compileEmbedded("plan.v1.json") })
var compiledDraftSchema = sync.OnceValues(func() (*jsonschema.Schema, error) { return compileEmbedded("draft.v1.json") })

func compileEmbedded(name string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineSchemaLoader{})
	for _, name := range []string{"template.v1.json", "plan.v1.json", "draft.v1.json"} {
		raw, err := schemas.ReadFile("schemas/" + name)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if err := c.AddResource("https://readyspec.dev/schemas/"+name, doc); err != nil {
			return nil, err
		}
	}
	return c.Compile("https://readyspec.dev/schemas/" + name)
}

// Load validates syntax, schema, topology and the declared semantic digest.
// All builtin implementations require an exact local registry match. A snapshot
// cannot acquire builtin behavior by re-signing its contents or naming a compiler.
func Load(r io.Reader) (*Plan, error) {
	doc, err := safeyaml.Parse(r)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: invalid JSON value")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	schema, err := compiledSchema()
	if err != nil {
		return nil, fmt.Errorf("INFRA_ERROR: compile embedded schema: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		// Schema errors can include user-supplied values; do not echo the document.
		return nil, fmt.Errorf("PLAN_SCHEMA_ERROR: document does not match readyspec.plan/v1")
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: decode plan")
	}
	templates := make(map[string]string, len(p.EmbeddedTemplates))
	parameters := make(map[string]map[string]any, len(p.EmbeddedTemplates))
	for _, value := range doc["embedded_templates"].([]any) {
		template := value.(map[string]any)
		implementation, _ := template["implementation"].(map[string]any)
		if (template["source"] == "BUILTIN" || implementation["kind"] == "BUILTIN") && !trustedTemplate(template) {
			return nil, fmt.Errorf("PLAN_ERROR: builtin template does not match the local trusted registry")
		}
		if template["execution_mode"] == "PRIVILEGED" && !trustedTemplate(template) {
			return nil, fmt.Errorf("PLAN_ERROR: privileged template is not in the local trusted registry")
		}
		key := template["template_id"].(string) + "@" + template["version"].(string)
		if _, exists := templates[key]; exists {
			return nil, fmt.Errorf("PLAN_ERROR: duplicate embedded template")
		}
		digest, err := semantic.TemplateDigest(template)
		if err != nil {
			return nil, fmt.Errorf("PLAN_ERROR: invalid template digest")
		}
		if template["digest"] != digest {
			return nil, fmt.Errorf("PLAN_ERROR: embedded template digest mismatch")
		}
		if p.TemplateLock[key] != digest {
			return nil, fmt.Errorf("PLAN_ERROR: template lock mismatch")
		}
		templates[key] = digest
		parameters[key] = template["params_schema"].(map[string]any)
		_, err = compileParameters(parameters[key])
		if err != nil {
			return nil, err
		}
	}
	if len(p.TemplateLock) != len(templates) {
		return nil, fmt.Errorf("PLAN_ERROR: template lock contains missing snapshots")
	}
	roleIDs := make(map[string]bool)
	itemIDs := make(map[string]bool)
	for _, role := range p.Roles {
		if roleIDs[role.RoleID] {
			return nil, fmt.Errorf("PLAN_ERROR: duplicate role_id")
		}
		roleIDs[role.RoleID] = true
		if role.MaxNodes != nil && role.MinNodes > *role.MaxNodes {
			return nil, fmt.Errorf("PLAN_ERROR: min_nodes exceeds max_nodes")
		}
		for _, item := range role.Items {
			if itemIDs[item.ItemID] {
				return nil, fmt.Errorf("PLAN_ERROR: duplicate item_id")
			}
			itemIDs[item.ItemID] = true
			if templates[item.TemplateID+"@"+item.TemplateVersion] != item.TemplateDigest {
				return nil, fmt.Errorf("PLAN_ERROR: item template reference mismatch")
			}
			for _, name := range item.ExplicitNullParams {
				if _, exists := item.Params[name]; exists {
					return nil, fmt.Errorf("PLAN_ERROR: parameter has both an explicit value and explicit null")
				}
			}
			if _, err := ResolveParameters(parameters[item.TemplateID+"@"+item.TemplateVersion], item, nil, nil); err != nil {
				return nil, err
			}
		}
	}
	digest, err := semantic.PlanDigest(doc)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: %w", err)
	}
	if p.Digest != "" && p.Digest != digest {
		return nil, fmt.Errorf("PLAN_DIGEST_MISMATCH: declared digest does not match content")
	}
	p.Digest = digest
	return &p, nil
}

func trustedTemplate(doc map[string]any) bool {
	return template.Trusted(doc)
}
