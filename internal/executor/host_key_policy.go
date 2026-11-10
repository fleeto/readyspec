package executor

import "fmt"

type HostKeyPolicy string

const (
	HostKeyRecordOnly HostKeyPolicy = "record-only"
	HostKeyStrict     HostKeyPolicy = "strict"
)

// ParseHostKeyPolicy keeps invalid settings from silently selecting a policy.
func ParseHostKeyPolicy(value string) (HostKeyPolicy, error) {
	switch HostKeyPolicy(value) {
	case "", HostKeyRecordOnly:
		return HostKeyRecordOnly, nil
	case HostKeyStrict:
		return HostKeyStrict, nil
	default:
		return "", fmt.Errorf("SSH_HOST_KEY_POLICY_INVALID: expected record-only or strict")
	}
}
