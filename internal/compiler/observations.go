package compiler

import (
	"fmt"
	"math/big"
	"regexp"

	"github.com/fleeto/readyspec/internal/plan"
)

// Observation-only properties collect evidence without adding a business
// assertion. They may share a stricter matcher used by another check item.
func nativeObservation(id, property string, values map[string]plan.ResolvedParam) bool {
	if id == "S02" {
		switch property {
		case "uid", "gid", "owner", "group", "mode":
			_, asserted := values[property]
			return !asserted
		}
	}
	if id == "S03" {
		switch property {
		case "source":
			return true
		case "filesystem":
			_, asserted := values["filesystem"]
			return !asserted
		case "opts":
			_, required := values["required_options"]
			_, forbidden := values["forbidden_options"]
			return !required && !forbidden
		}
	}
	return false
}

func validObservation(property string, actual any) bool {
	switch property {
	case "uid", "gid":
		n, ok := new(big.Rat).SetString(fmt.Sprint(actual))
		return ok && n.IsInt() && n.Sign() >= 0
	case "mode":
		value, ok := actual.(string)
		return ok && regexp.MustCompile(`^0[0-7]{3,4}$`).MatchString(value)
	case "opts":
		items, ok := actual.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			value, ok := item.(string)
			if !ok || value == "" {
				return false
			}
		}
		return true
	default:
		value, ok := actual.(string)
		return ok && value != ""
	}
}
