package plan

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type ResolvedParam struct {
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// ResolveParameters is shared by static validation and host compilation.
// Groups and host overrides are already selected for this item by the caller.
func ResolveParameters(definition map[string]any, item CheckItem, groups map[string]map[string]any, host map[string]any) (map[string]ResolvedParam, error) {
	p, err := compileParameters(definition)
	if err != nil {
		return nil, err
	}
	values := make(map[string]ResolvedParam)
	for name, value := range p.defaults {
		values[name] = ResolvedParam{value, "default"}
	}
	for name, value := range item.Params {
		if !p.names[name] {
			return nil, fmt.Errorf("PLAN_PARAMETER_ERROR: item %s contains an undeclared parameter", item.ItemID)
		}
		values[name] = ResolvedParam{value, "item"}
	}
	for _, name := range item.ExplicitNullParams {
		if _, exists := item.Params[name]; exists {
			return nil, fmt.Errorf("PLAN_PARAMETER_ERROR: conflicting explicit null")
		}
		if !p.names[name] {
			return nil, fmt.Errorf("PLAN_PARAMETER_ERROR: undeclared explicit null")
		}
		values[name] = ResolvedParam{nil, "item"}
	}
	open := make(map[string]bool)
	for _, name := range item.OverridableParams {
		if !p.names[name] {
			return nil, fmt.Errorf("PLAN_PARAMETER_ERROR: undeclared override")
		}
		open[name] = true
	}
	keys := make([]string, 0, len(groups))
	for group := range groups {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	groupValues := make(map[string]any)
	for _, group := range keys {
		for name, value := range groups[group] {
			if !open[name] {
				return nil, fmt.Errorf("OVERRIDE_REJECTED: item %s parameter %s", item.ItemID, name)
			}
			normalized, err := normalizeParam(definition, name, value)
			if err != nil {
				return nil, fmt.Errorf("PLAN_PARAMETER_ERROR: invalid domain value for %s", name)
			}
			if previous, exists := groupValues[name]; exists && !reflect.DeepEqual(previous, normalized) {
				if _, disambiguated := host[name]; !disambiguated {
					return nil, fmt.Errorf("OVERRIDE_CONFLICT: item %s parameter %s", item.ItemID, name)
				}
			}
			groupValues[name] = normalized
			values[name] = ResolvedParam{value, "group:" + group}
		}
	}
	for name, value := range host {
		if !open[name] {
			return nil, fmt.Errorf("OVERRIDE_REJECTED: item %s parameter %s", item.ItemID, name)
		}
		values[name] = ResolvedParam{value, "host"}
	}
	effective := make(map[string]any, len(values))
	for name, value := range values {
		effective[name] = value.Value
	}
	if err := p.schema.Validate(effective); err != nil {
		required, _ := definition["required"].([]any)
		if names, ok := definition["required"].([]string); ok {
			for _, name := range names {
				required = append(required, name)
			}
		}
		for _, key := range required {
			if name, ok := key.(string); ok {
				if _, exists := effective[name]; !exists {
					return nil, parameterError("REQUIRED", name, item)
				}
			}
		}
		return nil, parameterError("PARAMETER_CONSTRAINT", "", item)
	}
	for name, value := range values {
		normalized, err := normalizeParam(definition, name, value.Value)
		if err != nil {
			code := "PARAMETER_FORMAT"
			properties, _ := definition["properties"].(map[string]any)
			property, _ := properties[name].(map[string]any)
			if property["x-readyspec-type"] == "capacity" {
				code = "CAPACITY_FORMAT"
				if text, ok := value.Value.(string); ok && capacityPattern.MatchString(strings.TrimSpace(text)) {
					code = "CAPACITY_RANGE"
				}
			}
			return nil, parameterError(code, name, item)
		}
		values[name] = ResolvedParam{normalized, value.Source}
	}
	if err := validateOrderedParameters(definition, values); err != nil {
		return nil, parameterError("PARAMETER_RANGE", "", item)
	}
	return values, nil
}
