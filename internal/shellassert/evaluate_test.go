package shellassert

import "testing"

func TestEvaluateTextAndNumericAssertions(t *testing.T) {
	assertions, err := BuildAssertions([]any{
		map[string]any{"stream": "stdout", "operator": "contains", "value": "ready"},
		map[string]any{"stream": "stderr", "operator": "equals", "value": ""},
		map[string]any{"stream": "stdout", "operator": "number_range", "extract_regex": `count=(\d+)`, "min": float64(2), "max": float64(4)},
	}, float64(0))
	if err != nil {
		t.Fatal(err)
	}
	result := Evaluate(assertions, Observation{ExitCode: 0, Stdout: "ready\ncount=3", Stderr: ""})
	if result.Status != "PASS" || len(result.Diagnostics) != 0 {
		t.Fatalf("expected passing evaluation, got %+v", result)
	}
}

func TestEvaluateKeepsTextExactAndDistinguishesError(t *testing.T) {
	assertions, err := BuildAssertions([]any{
		map[string]any{"stream": "stdout", "operator": "equals", "value": "ready\n"},
		map[string]any{"stream": "stdout", "operator": "not_contains", "value": "failed"},
	}, float64(127))
	if err != nil {
		t.Fatal(err)
	}
	if result := Evaluate(assertions, Observation{ExitCode: 127, Stdout: "ready\n"}); result.Status != "PASS" {
		t.Fatalf("exit 127 must remain an ordinary observed exit code: %+v", result)
	}
	if result := Evaluate(assertions, Observation{ExitCode: 127, Stdout: "ready\n", ExecutionError: "shell launch failed"}); result.Status != "ERROR" {
		t.Fatalf("typed execution failure must be ERROR: %+v", result)
	}
}

func TestEvaluateRejectsTruncatedAndOversizedObservations(t *testing.T) {
	assertions, err := BuildAssertions(nil, float64(0))
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range []Observation{
		{ExitCode: 0, Truncated: true},
		{ExitCode: 0, Stdout: string(make([]byte, MaxTextBytes+1))},
		{ExitCode: 0, TimedOut: true},
	} {
		if result := Evaluate(assertions, observation); result.Status != "ERROR" {
			t.Fatalf("expected bounded collection error, got %+v", result)
		}
	}
}

func TestBuildAssertionsRejectsConflictingExitAndOversizedPattern(t *testing.T) {
	if _, err := BuildAssertions([]any{map[string]any{"stream": "exit_code", "operator": "equals", "value": float64(1)}}, float64(0)); err == nil {
		t.Fatal("conflicting exit code assertions were accepted")
	}
	if _, err := BuildAssertions([]any{map[string]any{"stream": "stdout", "operator": "regex", "pattern": string(make([]byte, 4097))}}, float64(0)); err == nil {
		t.Fatal("oversized regular expression was accepted")
	}
}
