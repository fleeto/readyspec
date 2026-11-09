package diagnostic_test

import (
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/diagnostic"
)

func TestRuntimeReasonCodesAreSafeAndPhaseIndependent(t *testing.T) {
	codes := []string{
		"DNS_LOOKUP_FAILED", "TCP_CONNECT_REFUSED", "TCP_CONNECT_TIMEOUT", "KNOWN_HOSTS_UNREADABLE",
		"HOST_KEY_UNKNOWN", "HOST_KEY_CHANGED", "HOST_KEY_REVOKED", "SSH_NEGOTIATION_FAILED",
		"SSH_AUTH_REJECTED", "AGENT_SELECTED_KEY_MISSING", "FACT_MACHINE_ID_MISSING", "FACT_BOOT_ID_INVALID",
		"CHECKSUM_TOOL_UNAVAILABLE", "SUDO_CREDENTIAL_MISSING", "SUDO_POLICY_DENIED", "SUDO_RESULT_UNVERIFIABLE",
		"REMOTE_TEMP_CANDIDATES_EXHAUSTED", "UPLOAD_FAILED", "PAYLOAD_DIGEST_MISMATCH", "REMOTE_COMMAND_TIMEOUT",
		"REMOTE_COMMAND_EXIT_UNEXPECTED", "OUTPUT_LIMIT_EXCEEDED", "RESULT_JSON_INVALID", "RESULT_RESOURCE_MISSING",
	}
	for _, code := range codes {
		item := diagnostic.ForCode(code, "node-a")
		if item.Code != code || item.Category == "" || item.Message == "" || item.Action == "" || item.HostID != "node-a" {
			t.Errorf("incomplete registered diagnostic for %s: %+v", code, item)
		}
		if strings.Contains(item.Message, "password wrong") || strings.Contains(item.Code, "PASSWORD_WRONG") {
			t.Errorf("unsafe password attribution in %s: %+v", code, item)
		}
	}
}

func TestUnknownCauseUsesFixedConservativeDiagnostic(t *testing.T) {
	item := diagnostic.ForCode("private stderr must not be copied", "node-a")
	if item.Code != "DIAGNOSTIC_UNCLASSIFIED" || item.Category != "EXECUTION_ERROR" || strings.Contains(item.Message, "private stderr") {
		t.Fatalf("unknown diagnostic is not conservative: %+v", item)
	}
}
