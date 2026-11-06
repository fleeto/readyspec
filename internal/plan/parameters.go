package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external parameter schema references are not allowed")
}

type parameterSchema struct {
	schema   *jsonschema.Schema
	names    map[string]bool
	defaults map[string]any
}

func compileParameters(definition map[string]any) (*parameterSchema, error) {
	raw, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("PLAN_PARAMETER_SCHEMA_ERROR: invalid parameter schema")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("PLAN_PARAMETER_SCHEMA_ERROR: invalid parameter schema")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(offlineSchemaLoader{})
	compiler.AssertFormat()
	const location = "https://readyspec.invalid/parameters.json"
	if err := compiler.AddResource(location, value); err != nil {
		return nil, fmt.Errorf("PLAN_PARAMETER_SCHEMA_ERROR: invalid parameter schema")
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("PLAN_PARAMETER_SCHEMA_ERROR: invalid or non-local parameter schema")
	}
	parameters := &parameterSchema{schema: schema, names: make(map[string]bool), defaults: make(map[string]any)}
	if err := parameters.collect(schema, make(map[*jsonschema.Schema]bool)); err != nil {
		return nil, err
	}
	return parameters, nil
}

func (p *parameterSchema) collect(schema *jsonschema.Schema, seen map[*jsonschema.Schema]bool) error {
	if schema == nil || seen[schema] {
		return nil
	}
	seen[schema] = true
	for name, property := range schema.Properties {
		p.names[name] = true
		visited := make(map[*jsonschema.Schema]bool)
		for current := property; current != nil && !visited[current]; current = current.Ref {
			visited[current] = true
			if current.Default == nil {
				continue
			}
			value := *current.Default
			if old, exists := p.defaults[name]; exists && !reflect.DeepEqual(old, value) {
				return fmt.Errorf("PLAN_PARAMETER_SCHEMA_ERROR: ambiguous parameter defaults")
			}
			p.defaults[name] = value
			break
		}
	}
	children := []*jsonschema.Schema{schema.Ref, schema.Then, schema.Else}
	children = append(children, schema.AllOf...)
	children = append(children, schema.AnyOf...)
	children = append(children, schema.OneOf...)
	for _, child := range children {
		if err := p.collect(child, seen); err != nil {
			return err
		}
	}
	return nil
}
