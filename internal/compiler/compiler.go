// Package compiler translates checks without executing commands or accessing targets.
package compiler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/shellassert"
	"github.com/fleeto/readyspec/internal/template"
)

type Origin struct {
	Type            string `json:"resource_type"`
	RoleID          string `json:"role_id"`
	ItemID          string `json:"item_id"`
	Resource        string `json:"resource"`
	Property        string `json:"property"`
	Expected        any    `json:"expected"`
	Collection      bool   `json:"collection,omitempty"`
	MissingIsError  bool   `json:"missing_is_error,omitempty"`
	Evaluator       string `json:"evaluator,omitempty"`
	Numeric         bool   `json:"numeric,omitempty"`
	Source          string `json:"source"`
	ObservationOnly bool   `json:"observation_only,omitempty"`
	Frame           string `json:"frame,omitempty"`
}

type ItemResult struct {
	RoleID           string            `json:"role_id"`
	ItemID           string            `json:"item_id"`
	Status           string            `json:"status"`
	TemplateID       string            `json:"template_id"`
	TemplateVersion  string            `json:"template_version"`
	ExecutionMode    string            `json:"execution_mode"`
	EvidenceMode     string            `json:"evidence_mode"`
	ParameterSources map[string]string `json:"parameter_sources,omitempty"`
	Evidence         []Evidence        `json:"evidence,omitempty"`
}

type Evidence struct {
	Resource        string `json:"resource"`
	Property        string `json:"property"`
	Expected        any    `json:"expected"`
	Actual          any    `json:"actual"`
	Error           bool   `json:"error"`
	Source          string `json:"source"`
	StartTime       string `json:"start_time,omitempty"`
	EndTime         string `json:"end_time,omitempty"`
	DurationNS      int64  `json:"duration_ns,omitempty"`
	ObservationOnly bool   `json:"observation_only,omitempty"`
}

type Artifact struct {
	ExecutionMode        string                       `json:"execution_mode"`
	Config               json.RawMessage              `json:"config"`
	ConfigDigest         string                       `json:"config_digest"`
	Origins              []Origin                     `json:"origins"`
	Items                []ItemResult                 `json:"items"`
	Digest               string                       `json:"digest"`
	RequiredCapabilities []string                     `json:"required_capabilities"`
	CapabilitiesVerified bool                         `json:"capabilities_verified"`
	ParameterSources     map[string]map[string]string `json:"parameter_sources"`
}

type HostInput struct {
	Roles []string `json:"roles"`
	// Item ID -> group name -> parameter values.
	GroupOverrides map[string]map[string]map[string]any `json:"group_overrides,omitempty"`
	HostOverrides  map[string]map[string]any            `json:"host_overrides,omitempty"`
	// Nil means static preview. A provided map must explicitly prove requirements.
	Capabilities map[string]bool `json:"capabilities,omitempty"`
}

// Compile treats the supplied roles as one compilation scope. Call CompileHost
// for actual host role bindings and field overrides.
func Compile(p *plan.Plan) (Artifact, error) {
	return compile(p, HostInput{}, "STANDARD")
}

func CompileHost(p *plan.Plan, input HostInput) (Artifact, error) {
	selected, err := selectRoles(p, input)
	if err != nil {
		return Artifact{}, err
	}
	return compile(selected, input, "STANDARD")
}

type ExecutionArtifacts struct {
	Standard   Artifact `json:"standard"`
	Privileged Artifact `json:"privileged"`
	Digest     string   `json:"digest"`
}

type RoleArtifacts struct {
	RoleID    string             `json:"role_id"`
	Artifacts ExecutionArtifacts `json:"artifacts"`
}

type PlanArtifacts struct {
	PlanDigest string          `json:"plan_digest"`
	Roles      []RoleArtifacts `json:"roles"`
	Digest     string          `json:"digest"`
}

// CompilePlan previews each role independently. Cross-role merging requires
// an explicit host binding through CompileExecutionModes.
func CompilePlan(p *plan.Plan) (PlanArtifacts, error) {
	result := PlanArtifacts{PlanDigest: p.Digest, Roles: []RoleArtifacts{}}
	for _, role := range p.Roles {
		artifacts, err := CompileExecutionModes(p, HostInput{Roles: []string{role.RoleID}})
		if err != nil {
			return result, err
		}
		result.Roles = append(result.Roles, RoleArtifacts{RoleID: role.RoleID, Artifacts: artifacts})
	}
	sort.Slice(result.Roles, func(i, j int) bool { return result.Roles[i].RoleID < result.Roles[j].RoleID })
	digest, err := artifactHash(result)
	result.Digest = digest
	return result, err
}

func CompileExecutionModes(p *plan.Plan, input HostInput) (ExecutionArtifacts, error) {
	selected, err := selectRoles(p, input)
	if err != nil {
		return ExecutionArtifacts{}, err
	}
	for _, role := range selected.Roles {
		for _, item := range role.Items {
			found := false
			for _, snapshot := range selected.EmbeddedTemplates {
				if snapshot.TemplateID == item.TemplateID && snapshot.Version == item.TemplateVersion {
					found = true
					if snapshot.ExecutionMode != "STANDARD" && snapshot.ExecutionMode != "PRIVILEGED" {
						return ExecutionArtifacts{}, fmt.Errorf("PLAN_ERROR: invalid execution mode")
					}
					break
				}
			}
			if !found {
				return ExecutionArtifacts{}, fmt.Errorf("PLAN_ERROR: missing template snapshot")
			}
		}
	}
	byMode := func(mode string) *plan.Plan {
		copy := *selected
		copy.Roles = nil
		for _, role := range selected.Roles {
			r := role
			r.Items = nil
			for _, item := range role.Items {
				for _, snapshot := range selected.EmbeddedTemplates {
					if snapshot.TemplateID == item.TemplateID && snapshot.Version == item.TemplateVersion && snapshot.ExecutionMode == mode {
						r.Items = append(r.Items, item)
						break
					}
				}
			}
			copy.Roles = append(copy.Roles, r)
		}
		return &copy
	}
	standard, err := compile(byMode("STANDARD"), input, "STANDARD")
	if err != nil {
		return ExecutionArtifacts{}, err
	}
	privileged, err := compile(byMode("PRIVILEGED"), input, "PRIVILEGED")
	if err != nil {
		return ExecutionArtifacts{}, err
	}
	result := ExecutionArtifacts{Standard: standard, Privileged: privileged}
	result.Digest, err = semantic.Digest(result)
	return result, err
}

func selectRoles(p *plan.Plan, input HostInput) (*plan.Plan, error) {
	selected := *p
	selected.Roles = nil
	seen := map[string]bool{}
	items := map[string]bool{}
	for _, id := range input.Roles {
		if seen[id] {
			return nil, fmt.Errorf("PLAN_ERROR: duplicate role binding")
		}
		seen[id] = true
		found := false
		for _, role := range p.Roles {
			if role.RoleID == id {
				selected.Roles = append(selected.Roles, role)
				found = true
				for _, item := range role.Items {
					items[item.ItemID] = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("PLAN_ERROR: unknown role binding")
		}
	}
	for id := range input.GroupOverrides {
		if !items[id] {
			return nil, fmt.Errorf("OVERRIDE_REJECTED: unknown check item")
		}
	}
	for id := range input.HostOverrides {
		if !items[id] {
			return nil, fmt.Errorf("OVERRIDE_REJECTED: unknown check item")
		}
	}
	return &selected, nil
}

func compile(p *plan.Plan, input HostInput, mode string) (Artifact, error) {
	a := Artifact{ExecutionMode: mode, Origins: []Origin{}, Items: []ItemResult{}, RequiredCapabilities: []string{}, CapabilitiesVerified: input.Capabilities != nil, ParameterSources: map[string]map[string]string{}}
	requiredCapabilities := map[string]bool{}
	files := map[string]map[string]any{}
	commands := map[string]map[string]any{}
	native := map[string]map[string]map[string]any{"file": files}
	for _, role := range p.Roles {
		for _, item := range role.Items {
			status := "ERROR"
			if !item.Enabled {
				status = "SKIP"
			}
			a.Items = append(a.Items, ItemResult{RoleID: role.RoleID, ItemID: item.ItemID, Status: status, TemplateID: item.TemplateID, TemplateVersion: item.TemplateVersion, ExecutionMode: mode})
			if !item.Enabled {
				for _, snapshot := range p.EmbeddedTemplates {
					if snapshot.TemplateID == item.TemplateID && snapshot.Version == item.TemplateVersion {
						a.Items[len(a.Items)-1].EvidenceMode = snapshot.EvidenceMode
						break
					}
				}
				continue
			}
			var snapshot *plan.CheckTemplate
			for i := range p.EmbeddedTemplates {
				t := &p.EmbeddedTemplates[i]
				if t.TemplateID == item.TemplateID && t.Version == item.TemplateVersion {
					snapshot = t
					break
				}
			}
			if snapshot == nil {
				return a, fmt.Errorf("PLAN_ERROR: missing template snapshot")
			}
			a.Items[len(a.Items)-1].EvidenceMode = snapshot.EvidenceMode
			if snapshot.ExecutionMode != mode {
				return a, fmt.Errorf("EXECUTION_MODE_REQUIRED: item %s requires %s", item.ItemID, snapshot.ExecutionMode)
			}
			raw, _ := json.Marshal(snapshot)
			var content map[string]any
			_ = json.Unmarshal(raw, &content)
			builtin := snapshot.Source == "BUILTIN" || snapshot.Implementation["kind"] == "BUILTIN"
			if builtin && (!template.Trusted(content) || item.TemplateDigest != snapshot.Digest) {
				return a, fmt.Errorf("PLAN_ERROR: untrusted builtin snapshot")
			}
			if requirements, ok := snapshot.Implementation["requires"].([]any); ok {
				for _, requirement := range requirements {
					key, ok := requirement.(string)
					if !ok {
						return a, fmt.Errorf("PLAN_ERROR: invalid capability definition")
					}
					requiredCapabilities[key] = true
					if input.Capabilities != nil && !input.Capabilities[key] {
						return a, &Diagnostic{Code: "CAPABILITY_UNAVAILABLE", Message: "目标能力未被证实可用", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key}
					}
				}
			}
			values, err := plan.ResolveParameters(snapshot.ParamsSchema, item, input.GroupOverrides[item.ItemID], input.HostOverrides[item.ItemID])
			if err != nil {
				return a, err
			}
			a.ParameterSources[item.ItemID] = map[string]string{}
			for name, value := range values {
				a.ParameterSources[item.ItemID][name] = value.Source
			}
			a.Items[len(a.Items)-1].ParameterSources = make(map[string]string, len(values))
			for name, value := range values {
				a.Items[len(a.Items)-1].ParameterSources[name] = value.Source
			}
			addOrigins := func(origins ...Origin) {
				for i := range origins {
					origins[i].Source = collectionSource(item.TemplateID, values)
				}
				a.Origins = append(a.Origins, origins...)
			}
			extraRequirements := []string{}
			if item.TemplateID == "S01" {
				key := resourceKey("path-metadata", role.RoleID, item.ItemID)
				command := "python3 -c " + shellQuote(pathMetadataProbe) + " " + shellQuote(values["path"].Value.(string)) + " " + shellQuote(fmt.Sprint(values["follow_symlinks"].Value))
				commands[key] = map[string]any{"exec": "/bin/sh -c " + shellQuote(command), "exit-status": 0, "stdout": map[string]any{"match-regexp": "(?s).*"}, "timeout": 10000}
				addOrigins(Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "exit-status", Expected: 0, Collection: true}, Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "stdout", Expected: map[string]any{}, Evaluator: "S01_METADATA"})
			}
			if item.TemplateID == "S01" && values["follow_symlinks"].Value == true {
				extraRequirements = append(extraRequirements, "tool:stat")
			}
			if item.TemplateID == "D01" {
				extraRequirements = append(extraRequirements, "tool:"+fmt.Sprint(values["manager"].Value))
			}
			if item.TemplateID == "D03" {
				runtime := fmt.Sprint(values["runtime"].Value)
				if runtime == "python" {
					runtime = "python3"
				}
				if path, ok := values["path"]; ok {
					extraRequirements = append(extraRequirements, "executable:"+fmt.Sprint(path.Value))
				} else {
					extraRequirements = append(extraRequirements, "tool:"+runtime)
				}
			}
			for _, key := range extraRequirements {
				requiredCapabilities[key] = true
				if input.Capabilities != nil && !input.Capabilities[key] {
					return a, &Diagnostic{Code: "CAPABILITY_UNAVAILABLE", Message: "目标能力未被证实可用", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key}
				}
			}
			if !builtin {
				if snapshot.ExecutionMode != "STANDARD" || snapshot.Implementation["kind"] != "CUSTOM_SHELL" {
					return a, fmt.Errorf("IMPLEMENTATION_UNAVAILABLE: item %s", item.ItemID)
				}
				script, ok := snapshot.Implementation["command"].(string)
				if !ok || strings.TrimSpace(script) == "" || strings.ContainsRune(script, 0) {
					return a, fmt.Errorf("PLAN_ERROR: invalid custom shell")
				}
				names := make([]string, 0, len(values))
				for name := range values {
					names = append(names, name)
				}
				sort.Strings(names)
				var prefix strings.Builder
				for _, name := range names {
					if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
						return a, fmt.Errorf("PLAN_ERROR: invalid shell parameter name")
					}
					value := values[name].Value
					var text string
					if s, ok := value.(string); ok {
						text = s
					} else {
						data, err := json.Marshal(value)
						if err != nil {
							return a, err
						}
						text = string(data)
					}
					if strings.ContainsRune(text, 0) {
						return a, fmt.Errorf("PLAN_ERROR: NUL in shell parameter")
					}
					fmt.Fprintf(&prefix, "RS_PARAM_%s=%s; export RS_PARAM_%s; readonly RS_PARAM_%s;\n", name, shellQuote(text), name, name)
				}
				key := resourceKey("check", role.RoleID, item.ItemID)
				expected := snapshot.Implementation["expect_exit_code"]
				if expected == nil {
					expected = 0
				}
				assertions, err := shellassert.BuildAssertions(snapshot.Implementation["assertions"], expected)
				if err != nil {
					return a, &Diagnostic{Code: "CUSTOM_ASSERTION_INVALID", Message: "自定义检查断言无效或与退出码冲突", RoleID: role.RoleID, ItemID: item.ItemID}
				}
				frame := "__READYSPEC_START_" + strings.TrimPrefix(key, "check-") + "__"
				command := prefix.String() + "printf '%s\\n' " + shellQuote(frame) + ";\n" + script
				commands[key] = map[string]any{"exec": "/bin/sh -c " + shellQuote(command), "exit-status": map[string]any{"ge": 0}, "stdout": map[string]any{"match-regexp": "(?s).*"}, "stderr": map[string]any{"match-regexp": "(?s).*"}, "timeout": 10000}
				observation := map[string]any{"assertions": assertions}
				addOrigins(
					Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "exit-status", Expected: observation, Evaluator: "CUSTOM_SHELL", Frame: frame},
					Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "stdout", Expected: observation, Evaluator: "CUSTOM_SHELL", Frame: frame, ObservationOnly: true},
					Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "stderr", Expected: observation, Evaluator: "CUSTOM_SHELL", Frame: frame, ObservationOnly: true},
				)
				continue
			}
			if resource, kind, key, properties, ok := nativeResource(item.TemplateID, values); ok {
				if native[resource] == nil {
					native[resource] = map[string]map[string]any{}
				}
				if native[resource][key] == nil {
					native[resource][key] = map[string]any{}
				}
				for property, expected := range properties {
					observationOnly := nativeObservation(item.TemplateID, property, values)
					useMatcher := true
					if previous, exists := native[resource][key][property]; exists && !reflect.DeepEqual(previous, expected) {
						existingObservationOnly, seenOrigin := true, false
						for _, origin := range a.Origins {
							if origin.Type == kind && origin.Resource == key && origin.Property == property {
								seenOrigin = true
								if !origin.ObservationOnly {
									existingObservationOnly = false
								}
							}
						}
						if observationOnly {
							useMatcher = false
						} else if !seenOrigin || !existingObservationOnly {
							return a, conflict(role.RoleID, item.ItemID, key, property, a.Origins)
						}
					}
					if useMatcher {
						native[resource][key][property] = expected
					}
					if !resourceSetting(property) {
						resultProperty := property
						if kind == "HTTP" {
							if property == "body" {
								resultProperty = "Body"
							}
							if property == "headers" {
								resultProperty = "Headers"
							}
						}
						numeric := false
						if item.TemplateID == "O03" {
							switch expected.(type) {
							case map[string]any, float64, int, int64, json.Number:
								numeric = true
							}
						}
						addOrigins(Origin{Type: kind, RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: resultProperty, Expected: expected, Numeric: numeric, MissingIsError: item.TemplateID == "S02" && property == "exists", ObservationOnly: observationOnly})
					}
				}
				if item.TemplateID == "N04" {
					budget := 10000
					if ms, err := strconv.Atoi(fmt.Sprint(values["timeout_ms"].Value)); err == nil {
						budget = ms + 2000
					}
					rcodeKey := resourceKey("dns-rcode", role.RoleID, item.ItemID)
					commands[rcodeKey] = map[string]any{"exec": "/bin/sh -c " + shellQuote(dnsRcodeCommand(values)), "exit-status": 0, "stdout": map[string]any{"match-regexp": "(?s).*"}, "timeout": budget}
					addOrigins(Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: rcodeKey, Property: "stdout", Expected: map[string]any{}, Evaluator: "N04_RCODE"})
				}
				continue
			}
			if command, ok := advancedCommand(item.TemplateID, values); ok {
				key := resourceKey("builtin", role.RoleID, item.ItemID)
				timeout := 10000
				if item.TemplateID == "N05" {
					budget, err := strconv.Atoi(fmt.Sprint(values["timeout_ms"].Value))
					if err != nil {
						return a, err
					}
					timeout = budget + 2000
				}
				commands[key] = map[string]any{"exec": "/bin/sh -c " + shellQuote(command), "exit-status": 0, "stdout": map[string]any{"match-regexp": "(?s).*"}, "timeout": timeout}
				expected := map[string]any{}
				for name, value := range values {
					expected[name] = value.Value
				}
				addOrigins(Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "exit-status", Expected: 0, Collection: true}, Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "stdout", Expected: expected, Evaluator: item.TemplateID})
				continue
			}
			if item.TemplateID != "S01" {
				command, matcher, err := numericCommand(item.TemplateID, values)
				if err != nil {
					return a, err
				}
				key := resourceKey("builtin", role.RoleID, item.ItemID)
				commands[key] = map[string]any{"exec": "/bin/sh -c " + shellQuote(command), "exit-status": 0, "stdout": matcher, "timeout": 10000}
				numeric := item.TemplateID == "H02" || item.TemplateID == "H03" || item.TemplateID == "H04" || item.TemplateID == "S04" || item.TemplateID == "S05"
				addOrigins(Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "exit-status", Expected: 0, Collection: true}, Origin{Type: "Command", RoleID: role.RoleID, ItemID: item.ItemID, Resource: key, Property: "stdout", Expected: matcher, Numeric: numeric})
				continue
			}
			if item.TemplateID != "S01" {
				return a, fmt.Errorf("IMPLEMENTATION_UNAVAILABLE: item %s", item.ItemID)
			}
			path, _ := values["path"].Value.(string)
			properties := map[string]any{"exists": values["exists"].Value}
			if kind, ok := values["filetype"]; ok {
				if properties["exists"] == false {
					return a, fmt.Errorf("PLAN_PARAMETER_ERROR: absent path cannot require a file type")
				}
				properties["filetype"] = kind.Value
			}
			if files[path] == nil {
				files[path] = map[string]any{}
			}
			keys := make([]string, 0, len(properties))
			for key := range properties {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, property := range keys {
				expected := properties[property]
				if old, exists := files[path][property]; exists && !reflect.DeepEqual(old, expected) {
					return a, conflict(role.RoleID, item.ItemID, path, property, a.Origins)
				}
				files[path][property] = expected
				addOrigins(Origin{Type: "File", RoleID: role.RoleID, ItemID: item.ItemID, Resource: path, Property: property, Expected: expected})
			}
		}
	}
	sort.Slice(a.Origins, func(i, j int) bool {
		x, _ := json.Marshal(a.Origins[i])
		y, _ := json.Marshal(a.Origins[j])
		return string(x) < string(y)
	})
	sort.Slice(a.Items, func(i, j int) bool {
		if a.Items[i].RoleID == a.Items[j].RoleID {
			return a.Items[i].ItemID < a.Items[j].ItemID
		}
		return a.Items[i].RoleID < a.Items[j].RoleID
	})
	config := map[string]any{}
	for resource, entries := range native {
		if len(entries) > 0 {
			config[resource] = entries
		}
	}
	if len(commands) > 0 {
		config["command"] = commands
	}
	a.Config, _ = json.Marshal(config)
	for capability := range requiredCapabilities {
		a.RequiredCapabilities = append(a.RequiredCapabilities, capability)
	}
	sort.Strings(a.RequiredCapabilities)
	// Goss evaluates Go templates before JSON decoding. Escape delimiters in
	// user strings so paths, scripts and values cannot invoke template functions.
	a.Config = []byte(strings.ReplaceAll(string(a.Config), "{{", `\u007b\u007b`))
	a.ConfigDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(a.Config))
	digest, err := artifactHash(a)
	a.Digest = digest
	return a, err
}

// Normalize requires one observation per compiled property. Missing, duplicate,
// skipped, or errored observations never establish a successful check.
func Normalize(a Artifact, raw []byte) ([]ItemResult, error) {
	if a.ConfigDigest != fmt.Sprintf("sha256:%x", sha256.Sum256(a.Config)) {
		return nil, fmt.Errorf("GOSS_RESULT_ERROR: configuration integrity mismatch")
	}
	declared := a.Digest
	a.Digest = ""
	actual, err := artifactHash(a)
	if err != nil || declared == "" || declared != actual {
		return nil, fmt.Errorf("GOSS_RESULT_ERROR: artifact integrity mismatch")
	}
	if len(raw) > 16*1024*1024 {
		return nil, fmt.Errorf("GOSS_RESULT_ERROR: response too large")
	}
	var report struct {
		Results []gossObservation `json:"results"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("GOSS_RESULT_ERROR: invalid JSON")
	}
	results := append([]ItemResult{}, a.Items...)
	for i := range results {
		if results[i].Status == "SKIP" {
			continue
		}
		customShell := false
		for _, origin := range a.Origins {
			if origin.RoleID == results[i].RoleID && origin.ItemID == results[i].ItemID && origin.Evaluator == "CUSTOM_SHELL" {
				customShell = true
				break
			}
		}
		if customShell {
			results[i] = normalizeCustomShell(results[i], a.Origins, report.Results)
			continue
		}
		status := "PASS"
		collectionError := false
		count := 0
		for _, origin := range a.Origins {
			if origin.RoleID != results[i].RoleID || origin.ItemID != results[i].ItemID {
				continue
			}
			count++
			matches := 0
			for _, observation := range report.Results {
				if observation.Type != origin.Type || observation.Resource != origin.Resource || observation.Property != origin.Property {
					continue
				}
				matches++
				// Goss v0.4.9 reports an absent mount as this explicit lookup
				// error. Keep access/parse errors distinct from a proven absence.
				mountAbsent := origin.Type == "Mount" && origin.Property == "exists" && string(observation.Err) == `"Mountpoint not found"`
				evidenceActual, evidenceExpected := observation.Matcher.Actual, origin.Expected
				if origin.Evaluator == "D03" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = runtimeEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "D02" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = commandMetadataEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "R01" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = serviceEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "R05" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = timeSyncEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "R04" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = limitsEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "R03" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = loadEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "L01" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = uptimeEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "N06" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = routeEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "N01" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = interfaceEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "N04_RCODE" {
					if text, ok := observation.Matcher.Actual.(string); ok {
						_, evidenceActual = dnsRcodeEvaluation(text)
					}
				}
				if origin.Evaluator == "R02" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = processEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "S01_METADATA" {
					if text, ok := observation.Matcher.Actual.(string); ok {
						_, evidenceActual = pathMetadataEvaluation(text)
					}
				}
				if origin.ObservationOnly {
					evidenceExpected = nil
				}
				if origin.Evaluator == "D05" {
					evidenceActual = redactedEvidence(evidenceActual)
					evidenceExpected = "***"
				}
				if origin.Evaluator == "L02" || origin.Evaluator == "L03" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = logEvaluation(origin.Evaluator, params, text)
						}
					}
				}
				if origin.Evaluator == "N05" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = boundedHTTPEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "H03" || origin.Evaluator == "H04" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = memoryEvaluation(origin.Evaluator, params, text)
						}
					}
				}
				if origin.Evaluator == "H01" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = architectureEvaluation(params, text)
						}
					}
				}
				if origin.Evaluator == "O01" || origin.Evaluator == "O02" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							if origin.Evaluator == "O01" {
								_, evidenceActual = osReleaseEvaluation(params, text)
							} else {
								_, evidenceActual = kernelEvaluation(params, text)
							}
						}
					}
				}
				if origin.Evaluator == "O03" || origin.Evaluator == "O04" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							if origin.Evaluator == "O03" {
								_, evidenceActual = sysctlEvaluation(params, text)
							} else {
								_, evidenceActual = moduleEvaluation(params, text)
							}
						}
					}
				}
				if origin.Evaluator == "S04" || origin.Evaluator == "S05" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = filesystemEvaluation(origin.Evaluator, params, text)
						}
					}
				}
				if origin.Evaluator == "A01" || origin.Evaluator == "A02" {
					if text, ok := observation.Matcher.Actual.(string); ok {
						evidenceActual = nssEvidence(text)
					}
				}
				if origin.Evaluator == "D04" {
					if params, ok := origin.Expected.(map[string]any); ok {
						if text, ok := observation.Matcher.Actual.(string); ok {
							_, evidenceActual = elfEvaluation(params, text)
						}
					}
				}
				results[i].Evidence = append(results[i].Evidence, Evidence{Resource: origin.Resource, Property: origin.Property, Expected: evidenceExpected, Actual: evidenceActual, Error: observation.Skipped || (len(observation.Err) > 0 && string(observation.Err) != "null"), Source: origin.Source, StartTime: observation.StartTime, EndTime: observation.EndTime, DurationNS: observation.Duration, ObservationOnly: origin.ObservationOnly})
				if mountAbsent {
					status = "FAIL"
				} else if observation.Successful == nil || observation.Skipped || (len(observation.Err) > 0 && string(observation.Err) != "null") {
					if origin.Collection {
						collectionError = true
					}
					if status != "FAIL" {
						status = "ERROR"
					}
				} else if origin.ObservationOnly {
					if !validObservation(origin.Property, observation.Matcher.Actual) {
						collectionError = true
					}
				} else if origin.Numeric {
					evaluated := exactNumericMatch(origin.Expected, observation.Matcher.Actual, origin.Type == "KernelParam")
					if evaluated == "FAIL" {
						status = "FAIL"
					} else if evaluated != "PASS" {
						collectionError = true
					}
				} else if !*observation.Successful {
					if origin.Collection || origin.MissingIsError {
						collectionError = true
						status = "ERROR"
					} else {
						status = "FAIL"
					}
				} else if origin.Evaluator != "" {
					evaluated := evaluateObservation(origin.Evaluator, origin.Expected, observation.Matcher.Actual)
					if evaluated == "FAIL" {
						status = "FAIL"
					} else if evaluated != "PASS" {
						collectionError = true
					}
				}
			}
			if matches != 1 && status != "FAIL" {
				status = "ERROR"
			}
			if matches != 1 && origin.Collection {
				collectionError = true
			}
		}
		if count == 0 || collectionError {
			status = "ERROR"
		}
		results[i].Status = status
	}
	return results, nil
}

type gossObservation struct {
	Resource   string          `json:"resource-id"`
	Type       string          `json:"resource-type"`
	Property   string          `json:"property"`
	Successful *bool           `json:"successful"`
	Skipped    bool            `json:"skipped"`
	Err        json.RawMessage `json:"err"`
	StartTime  string          `json:"start-time"`
	EndTime    string          `json:"end-time"`
	Duration   int64           `json:"duration"`
	Matcher    struct {
		Actual any `json:"actual"`
	} `json:"matcher-result"`
}

func normalizeCustomShell(result ItemResult, origins []Origin, observations []gossObservation) ItemResult {
	var selected []Origin
	for _, origin := range origins {
		if origin.RoleID == result.RoleID && origin.ItemID == result.ItemID && origin.Evaluator == "CUSTOM_SHELL" {
			selected = append(selected, origin)
		}
	}
	if len(selected) != 3 {
		result.Status = "ERROR"
		return result
	}
	actual := map[string]gossObservation{}
	for _, origin := range selected {
		var found []gossObservation
		for _, observation := range observations {
			if observation.Type == origin.Type && observation.Resource == origin.Resource && observation.Property == origin.Property {
				found = append(found, observation)
			}
		}
		if len(found) != 1 || found[0].Skipped || found[0].Successful == nil || len(found[0].Err) > 0 && string(found[0].Err) != "null" {
			result.Status = "ERROR"
			result.Evidence = append(result.Evidence, Evidence{Resource: origin.Resource, Property: origin.Property, Expected: nil, Actual: "观察缺失或执行失败", Error: true, Source: origin.Source, StartTime: firstTime(found, true), EndTime: firstTime(found, false)})
			return result
		}
		actual[origin.Property] = found[0]
	}
	exitValue, ok := actual["exit-status"].Matcher.Actual.(float64)
	stdout, stdoutOK := actual["stdout"].Matcher.Actual.(string)
	stderr, stderrOK := actual["stderr"].Matcher.Actual.(string)
	frame := selected[0].Frame + "\n"
	if !ok || !stdoutOK || !stderrOK || !strings.HasPrefix(stdout, frame) {
		result.Status = "ERROR"
		for _, property := range []string{"exit-status", "stdout", "stderr"} {
			observation := actual[property]
			value := observation.Matcher.Actual
			if property == "stdout" {
				value = "无法确认受控 Shell 已启动"
			}
			result.Evidence = append(result.Evidence, Evidence{Resource: observation.Resource, Property: property, Actual: value, Error: true, Source: selected[0].Source, StartTime: observation.StartTime, EndTime: observation.EndTime, DurationNS: observation.Duration})
		}
		return result
	}
	stdout = strings.TrimPrefix(stdout, frame)
	expected, _ := selected[0].Expected.(map[string]any)
	assertions, _ := expected["assertions"].([]map[string]any)
	if assertions == nil {
		if values, ok := expected["assertions"].([]any); ok {
			for _, value := range values {
				if assertion, ok := value.(map[string]any); ok {
					assertions = append(assertions, assertion)
				}
			}
		}
	}
	if exitValue < 0 || exitValue > 255 || exitValue != float64(int(exitValue)) {
		result.Status = "ERROR"
		return result
	}
	evaluated := shellassert.Evaluate(assertions, shellassert.Observation{ExitCode: int(exitValue), Stdout: stdout, Stderr: stderr})
	result.Status = evaluated.Status
	for _, property := range []string{"exit-status", "stdout", "stderr"} {
		observation := actual[property]
		value := observation.Matcher.Actual
		if property == "stdout" {
			value = stdout
		}
		result.Evidence = append(result.Evidence, Evidence{Resource: observation.Resource, Property: property, Expected: assertions, Actual: value, Error: evaluated.Status == "ERROR", Source: selected[0].Source, StartTime: observation.StartTime, EndTime: observation.EndTime, DurationNS: observation.Duration, ObservationOnly: property != "exit-status"})
	}
	return result
}

func firstTime(observations []gossObservation, start bool) string {
	if len(observations) == 0 {
		return ""
	}
	if start {
		return observations[0].StartTime
	}
	return observations[0].EndTime
}

func numericCommand(id string, values map[string]plan.ResolvedParam) (string, map[string]any, error) {
	switch id {
	case "A03":
		key := values["key"].Value.(string)
		expected := values["expected"].Value.(string)
		command := `/usr/sbin/sshd -T`
		if context, ok := values["match_context"]; ok {
			command += ` -C ` + shellQuote(context.Value.(string))
		}
		return command, map[string]any{"match-regexp": "(?m)^" + regexp.QuoteMeta(key) + `[ \t]+` + regexp.QuoteMeta(expected) + `[ \t]*$`}, nil
	case "H02":
		return `n=$(getconf _NPROCESSORS_ONLN) || exit 2; case "$n" in ''|*[!0-9]*) exit 2;; esac; printf '%s\n' "$n"`, map[string]any{"ge": values["min_cores"].Value}, nil
	default:
		return "", nil, fmt.Errorf("IMPLEMENTATION_UNAVAILABLE: template %s", id)
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func resourceKey(prefix, role, item string) string {
	digest, _ := semantic.Digest([]string{role, item})
	return prefix + "-" + strings.TrimPrefix(digest, "sha256:")
}

func artifactHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), nil
}

func (a *Artifact) UnmarshalJSON(raw []byte) error {
	type plain Artifact
	var decoded plain
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*a = Artifact(decoded)
	return nil
}

func nativeResource(id string, values map[string]plan.ResolvedParam) (string, string, string, map[string]any, bool) {
	switch id {
	case "S03":
		properties := map[string]any{"exists": true, "source": map[string]any{"match-regexp": ".+"}, "filesystem": map[string]any{"match-regexp": ".+"}, "opts": map[string]any{"have-len": map[string]any{"ge": 0}}}
		if filesystem, ok := values["filesystem"]; ok {
			properties["filesystem"] = filesystem.Value
		}
		matchers := []any{}
		if required, ok := values["required_options"]; ok {
			matchers = append(matchers, map[string]any{"contain-elements": required.Value})
		}
		if forbidden, ok := values["forbidden_options"]; ok {
			items, _ := forbidden.Value.([]any)
			for _, item := range items {
				matchers = append(matchers, map[string]any{"not": map[string]any{"contain-element": item}})
			}
		}
		if len(matchers) > 0 {
			properties["opts"] = map[string]any{"and": matchers}
		}
		return "mount", "Mount", values["path"].Value.(string), properties, true
	case "N04":
		key := values["record_type"].Value.(string) + ":" + values["name"].Value.(string)
		return "dns", "DNS", key, map[string]any{"resolvable": true, "addrs": values["addrs"].Value, "server": values["server"].Value, "timeout": values["timeout_ms"].Value}, true
	case "S02":
		properties := map[string]any{"exists": true, "uid": map[string]any{"ge": 0}, "gid": map[string]any{"ge": 0}, "owner": map[string]any{"match-regexp": ".+"}, "group": map[string]any{"match-regexp": ".+"}, "mode": map[string]any{"match-regexp": "^0[0-7]{3,4}$"}}
		for _, name := range []string{"mode", "owner", "group"} {
			if value, ok := values[name]; ok {
				properties[name] = value.Value
			}
		}
		return "file", "File", values["path"].Value.(string), properties, true
	default:
		return "", "", "", nil, false
	}
}

func resourceSetting(property string) bool {
	switch property {
	case "timeout", "method", "allow-insecure", "no-follow-redirects", "server":
		return true
	default:
		return false
	}
}
