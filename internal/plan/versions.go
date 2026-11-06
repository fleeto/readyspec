package plan

import (
	"regexp"
	"strconv"
	"strings"
)

var numericVersionPrefix = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)(?:[-+_].*)?$`)

// CompareNumericVersions compares numeric release components, retaining no
// assumptions about Debian/RPM or SemVer prerelease ordering.
func CompareNumericVersions(actual, expected string) (int, bool) {
	parse := func(text string) ([]uint64, bool) {
		match := numericVersionPrefix.FindStringSubmatch(text)
		if len(match) < 2 {
			return nil, false
		}
		parts := strings.Split(match[1], ".")
		result := make([]uint64, len(parts))
		for i, part := range parts {
			n, err := strconv.ParseUint(part, 10, 64)
			if err != nil {
				return nil, false
			}
			result[i] = n
		}
		return result, true
	}
	a, ok := parse(actual)
	if !ok {
		return 0, false
	}
	b, ok := parse(expected)
	if !ok {
		return 0, false
	}
	length := len(a)
	if len(b) > length {
		length = len(b)
	}
	for i := 0; i < length; i++ {
		var x, y uint64
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1, true
		}
		if x > y {
			return 1, true
		}
	}
	return 0, true
}
