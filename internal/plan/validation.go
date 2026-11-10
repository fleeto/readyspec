package plan

import (
	"fmt"
	"strings"
)

// ParameterError identifies a safe field without retaining the submitted value.
type ParameterError struct {
	Code   string
	Field  string
	RoleID string
	ItemID string
}

func (e *ParameterError) Error() string { return fmt.Sprintf("PLAN_PARAMETER_ERROR: %s", e.Code) }

func parameterError(code, name string, item CheckItem) *ParameterError {
	field := "/params"
	if name != "" {
		field += "/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
	}
	return &ParameterError{Code: code, Field: field, ItemID: item.ItemID}
}
