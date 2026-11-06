// Package shellassert evaluates bounded assertions over captured command output.
package shellassert

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const MaxTextBytes = 64 * 1024

type Observation struct {
	ExitCode       int    `json:"exit_code"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	ExecutionError string `json:"execution_error,omitempty"`
	TimedOut       bool   `json:"timed_out,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
}

type Result struct {
	Status      string   `json:"status"`
	Diagnostics []string `json:"diagnostics"`
}

func BuildAssertions(raw any, expectedExit any) ([]map[string]any, error) {
	result := make([]map[string]any, 0)
	if raw != nil {
		items, ok := raw.([]any)
		if !ok || len(items) > 64 {
			return nil, fmt.Errorf("invalid custom assertions")
		}
		for _, item := range items {
			assertion, ok := item.(map[string]any)
			if !ok || !validAssertion(assertion) {
				return nil, fmt.Errorf("invalid custom assertion")
			}
			result = append(result, assertion)
		}
	}
	exit, ok := integer(expectedExit)
	if !ok {
		return nil, fmt.Errorf("invalid custom exit code")
	}
	for _, assertion := range result {
		if assertion["stream"] == "exit_code" {
			value, valid := integer(assertion["value"])
			if !valid || assertion["operator"] != "equals" || value != exit {
				return nil, fmt.Errorf("custom exit code assertion conflicts with expect_exit_code")
			}
		}
	}
	result = append(result, map[string]any{"stream": "exit_code", "operator": "equals", "value": exit})
	return result, nil
}

func validAssertion(assertion map[string]any) bool {
	stream, _ := assertion["stream"].(string)
	operator, _ := assertion["operator"].(string)
	switch stream {
	case "exit_code":
		_, valid := integer(assertion["value"])
		return operator == "equals" && valid
	case "stdout", "stderr":
	default:
		return false
	}
	switch operator {
	case "equals", "contains", "not_contains":
		_, ok := assertion["value"].(string)
		return ok
	case "regex":
		pattern, ok := assertion["pattern"].(string)
		if !ok || len(pattern) > 4096 {
			return false
		}
		_, err := regexp.Compile(pattern)
		return err == nil
	case "number_equals", "number_not_equals", "number_gt", "number_gte", "number_lt", "number_lte":
		_, ok := finite(assertion["value"])
		if !ok {
			return false
		}
		return validExtractRegex(assertion)
	case "number_range":
		minimum, minOK := finite(assertion["min"])
		maximum, maxOK := finite(assertion["max"])
		return minOK && maxOK && minimum <= maximum && validExtractRegex(assertion)
	default:
		return false
	}
}

func validExtractRegex(assertion map[string]any) bool {
	pattern, exists := assertion["extract_regex"]
	if !exists {
		return true
	}
	text, ok := pattern.(string)
	if !ok || len(text) > 4096 {
		return false
	}
	_, err := regexp.Compile(text)
	return err == nil
}

// Evaluate applies every assertion. It never executes a command or infers a
// launch failure from an exit-code value or localized stderr text.
func Evaluate(assertions []map[string]any, observation Observation) Result {
	if observation.ExecutionError != "" || observation.TimedOut || observation.Truncated ||
		len(observation.Stdout) > MaxTextBytes || len(observation.Stderr) > MaxTextBytes {
		return Result{Status: "ERROR", Diagnostics: []string{"采集失败、超时或输出超过 64 KiB。"}}
	}
	if len(assertions) == 0 {
		return Result{Status: "ERROR", Diagnostics: []string{"没有可执行的基础断言。"}}
	}
	result := Result{Status: "PASS", Diagnostics: []string{}}
	for index, assertion := range assertions {
		ok, valid := evaluate(assertion, observation)
		if !valid {
			result.Status = "ERROR"
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("第 %d 条断言格式无效。", index+1))
			continue
		}
		if !ok {
			if result.Status != "ERROR" {
				result.Status = "FAIL"
			}
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("第 %d 条断言未满足。", index+1))
		}
	}
	return result
}

func evaluate(assertion map[string]any, observation Observation) (bool, bool) {
	stream, ok := assertion["stream"].(string)
	if !ok {
		return false, false
	}
	operator, ok := assertion["operator"].(string)
	if !ok {
		return false, false
	}
	switch stream {
	case "exit_code":
		if operator != "equals" {
			return false, false
		}
		expected, ok := integer(assertion["value"])
		return ok && expected == observation.ExitCode, ok
	case "stdout", "stderr":
	default:
		return false, false
	}
	actual := observation.Stdout
	if stream == "stderr" {
		actual = observation.Stderr
	}
	switch operator {
	case "contains", "not_contains", "equals":
		expected, ok := assertion["value"].(string)
		if !ok {
			return false, false
		}
		switch operator {
		case "contains":
			return strings.Contains(actual, expected), true
		case "not_contains":
			return !strings.Contains(actual, expected), true
		default:
			return actual == expected, true
		}
	case "regex":
		pattern, ok := assertion["pattern"].(string)
		if !ok {
			return false, false
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return false, false
		}
		return compiled.MatchString(actual), true
	case "number_equals", "number_not_equals", "number_gt", "number_gte", "number_lt", "number_lte", "number_range":
		pattern, _ := assertion["extract_regex"].(string)
		number, ok := extractNumber(actual, pattern)
		if !ok {
			return false, false
		}
		if operator == "number_range" {
			minimum, minOK := finite(assertion["min"])
			maximum, maxOK := finite(assertion["max"])
			if !minOK || !maxOK || minimum > maximum {
				return false, false
			}
			return number >= minimum && number <= maximum, true
		}
		expected, ok := finite(assertion["value"])
		if !ok {
			return false, false
		}
		switch operator {
		case "number_equals":
			return number == expected, true
		case "number_not_equals":
			return number != expected, true
		case "number_gt":
			return number > expected, true
		case "number_gte":
			return number >= expected, true
		case "number_lt":
			return number < expected, true
		default:
			return number <= expected, true
		}
	default:
		return false, false
	}
}

func extractNumber(text, pattern string) (float64, bool) {
	match := text
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return 0, false
		}
		matches := re.FindAllStringSubmatch(text, 2)
		if len(matches) != 1 {
			return 0, false
		}
		if re.NumSubexp() > 0 {
			if len(matches[0]) < 2 {
				return 0, false
			}
			match = matches[0][1]
		} else {
			match = matches[0][0]
		}
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(match), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func finite(value any) (float64, bool) {
	var number float64
	switch n := value.(type) {
	case float64:
		number = n
	case float32:
		number = float64(n)
	case int:
		number = float64(n)
	case int64:
		number = float64(n)
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func integer(value any) (int, bool) {
	number, ok := finite(value)
	if !ok || number < 0 || number > 255 || math.Trunc(number) != number {
		return 0, false
	}
	return int(number), true
}
