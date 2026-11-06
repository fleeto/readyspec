// Package safeyaml parses the JSON-compatible subset of YAML used by ReadySpec.
package safeyaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const MaxBytes = 10 * 1024 * 1024

// Parse reads one bounded, acyclic, JSON-compatible YAML object. Errors contain
// positions rather than source excerpts, which may contain credentials.
func Parse(r io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("YAML_READ: %w", err)
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("YAML_SIZE_EXCEEDED: limit is %d bytes", MaxBytes)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("YAML_INVALID_UTF8")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("YAML_EMPTY")
		}
		return nil, fmt.Errorf("YAML_SYNTAX_ERROR: invalid YAML document")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("YAML_MULTIPLE_DOCUMENTS: expected exactly one document")
	}
	state := traversal{active: make(map[*yaml.Node]bool)}
	value, err := state.convert(document.Content[0], 0)
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("YAML_ROOT_TYPE: expected an object")
	}
	return result, nil
}

type traversal struct {
	active         map[*yaml.Node]bool
	nodes, aliases int
	expandedBytes  int
}

func (s *traversal) convert(node *yaml.Node, depth int) (any, error) {
	s.expandedBytes += len(node.Value)
	if s.expandedBytes > MaxBytes {
		return nil, fmt.Errorf("YAML_EXPANSION_LIMIT: expanded scalar byte budget exceeded")
	}
	if s.active[node] {
		return nil, fmt.Errorf("YAML_CYCLE: line %d", node.Line)
	}
	if depth > 64 {
		return nil, fmt.Errorf("YAML_DEPTH_LIMIT: maximum depth is 64")
	}
	s.nodes++
	if s.nodes > 100000 {
		return nil, fmt.Errorf("YAML_EXPANSION_LIMIT: expanded node budget exceeded")
	}
	s.active[node] = true
	defer delete(s.active, node)
	switch node.Kind {
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			return nil, fmt.Errorf("YAML_TAG: line %d", node.Line)
		}
		result := make(map[string]any, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			s.expandedBytes += len(key.Value)
			if s.expandedBytes > MaxBytes {
				return nil, fmt.Errorf("YAML_EXPANSION_LIMIT: expanded scalar byte budget exceeded")
			}
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, fmt.Errorf("YAML_KEY_TYPE: line %d: expected a string key", key.Line)
			}
			if _, exists := result[key.Value]; exists {
				return nil, fmt.Errorf("YAML_DUPLICATE_KEY: line %d, column %d", key.Line, key.Column)
			}
			value, err := s.convert(node.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			result[key.Value] = value
		}
		return result, nil
	case yaml.SequenceNode:
		if node.Tag != "!!seq" {
			return nil, fmt.Errorf("YAML_TAG: line %d", node.Line)
		}
		result := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := s.convert(child, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!timestamp":
			return node.Value, nil
		case "!!null", "!!bool", "!!int", "!!float":
		default:
			return nil, fmt.Errorf("YAML_TAG: line %d", node.Line)
		}
		var value any
		if err := node.Decode(&value); err != nil {
			return nil, fmt.Errorf("YAML_SCALAR: line %d: invalid scalar", node.Line)
		}
		switch number := value.(type) {
		case int:
			if number < -9007199254740991 || number > 9007199254740991 {
				return nil, fmt.Errorf("YAML_NUMBER: line %d: integer outside interoperable range", node.Line)
			}
		case uint64:
			if number > 9007199254740991 {
				return nil, fmt.Errorf("YAML_NUMBER: line %d: integer outside interoperable range", node.Line)
			}
		case float64:
			if math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, fmt.Errorf("YAML_NUMBER: line %d: nonfinite number", node.Line)
			}
		}
		return value, nil
	case yaml.AliasNode:
		s.aliases++
		if s.aliases > 100 {
			return nil, fmt.Errorf("YAML_ALIAS_LIMIT: maximum alias expansions is 100")
		}
		return s.convert(node.Alias, depth+1)
	default:
		return nil, fmt.Errorf("YAML_NODE_TYPE: line %d: unsupported node", node.Line)
	}
}
