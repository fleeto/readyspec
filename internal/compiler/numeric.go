package compiler

import (
	"fmt"
	"math/big"
	"strings"
)

func exactNumericMatch(expected any, actual any, allowNegative bool) string {
	value, ok := new(big.Rat).SetString(strings.TrimSpace(fmt.Sprint(actual)))
	if !ok || (!allowNegative && value.Sign() < 0) {
		return "ERROR"
	}
	var match func(any) (bool, bool)
	match = func(rule any) (bool, bool) {
		properties, ok := rule.(map[string]any)
		if !ok {
			bound, valid := new(big.Rat).SetString(fmt.Sprint(rule))
			return valid && value.Cmp(bound) == 0, valid
		}
		for operation, operand := range properties {
			if operation == "and" {
				rules, ok := operand.([]any)
				if !ok {
					return false, false
				}
				for _, child := range rules {
					passed, valid := match(child)
					if !valid {
						return false, false
					}
					if !passed {
						return false, true
					}
				}
				continue
			}
			bound, ok := new(big.Rat).SetString(fmt.Sprint(operand))
			if !ok {
				return false, false
			}
			comparison := value.Cmp(bound)
			passed := false
			switch operation {
			case "ge":
				passed = comparison >= 0
			case "gt":
				passed = comparison > 0
			case "le":
				passed = comparison <= 0
			case "lt":
				passed = comparison < 0
			default:
				return false, false
			}
			if !passed {
				return false, true
			}
		}
		return true, true
	}
	passed, valid := match(expected)
	if !valid {
		return "ERROR"
	}
	if passed {
		return "PASS"
	}
	return "FAIL"
}
