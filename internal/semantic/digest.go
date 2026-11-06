// Package semantic implements RFC 8785 semantic digests shared by plan files.
package semantic

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return jsoncanonicalizer.Transform(raw)
}

func Digest(value any) (string, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(canonical)), nil
}

func TemplateDigest(template map[string]any) (string, error) {
	cleaned := make(map[string]any, len(template))
	for key, value := range template {
		if key != "digest" {
			cleaned[key] = value
		}
	}
	return Digest(cleaned)
}

func PlanDigest(plan map[string]any) (string, error) {
	cleaned := make(map[string]any, len(plan))
	for key, value := range plan {
		switch key {
		case "digest", "updated_at", "release_manifest":
			continue
		}
		cleaned[key] = value
	}
	if templates, exists := cleaned["embedded_templates"]; exists {
		items, ok := templates.([]any)
		if !ok {
			return "", fmt.Errorf("embedded_templates must be an array")
		}
		sorted := append([]any{}, items...)
		keys := make([]string, len(items))
		// Validate before sorting so malformed input cannot cause a type assertion panic.
		for i, item := range sorted {
			template, ok := item.(map[string]any)
			if !ok {
				return "", fmt.Errorf("embedded_templates[%d] must be an object", i)
			}
			id, idOK := template["template_id"].(string)
			version, versionOK := template["version"].(string)
			if !idOK || !versionOK {
				return "", fmt.Errorf("embedded_templates[%d] requires template_id and version", i)
			}
			keys[i] = id + "@" + version
		}
		// Each entry carries its key because maps themselves are not comparable.
		type entry struct {
			key   string
			value any
		}
		entries := make([]entry, len(sorted))
		for i, value := range sorted {
			entries[i] = entry{keys[i], value}
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		for i, item := range entries {
			sorted[i] = item.value
		}
		cleaned["embedded_templates"] = sorted
	}
	return Digest(cleaned)
}
