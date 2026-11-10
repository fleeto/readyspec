// Package diagnostic defines safe, user-facing operational diagnostics.
package diagnostic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

type Error struct {
	Code   string
	HostID string
}

func (e *Error) Error() string { return fmt.Sprintf("AUTH_ERROR: %s", e.Code) }

// Item contains only allowlisted fields. Message and Action are registry values,
// never copied from an underlying error.
type Item struct {
	Category  string `json:"category"`
	Code      string `json:"code"`
	Phase     string `json:"phase"`
	HostID    string `json:"host_id,omitempty"`
	RoleID    string `json:"role_id,omitempty"`
	ItemID    string `json:"item_id,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Property  string `json:"property,omitempty"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Action    string `json:"action"`
}

// NewID returns an opaque 128-bit identifier encoded as lowercase hex.
func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func ForCode(code, hostID string) Item {
	item := Item{Phase: "SUBMISSION", HostID: hostID}
	switch code {
	case "CREDENTIAL_HOST_MISSING":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "该执行主机没有对应的认证凭据。", "CHECK_HOST_CREDENTIAL_BINDING"
	case "CREDENTIAL_METHOD_MISMATCH":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "主机凭据类型与所选认证方式不一致。", "CHECK_AUTHENTICATION_METHOD"
	case "CREDENTIAL_PASSWORD_EMPTY":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "该主机提交的 SSH 密码为空。", "ENTER_SSH_PASSWORD"
	case "PRIVATE_KEY_PARSE_FAILED":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "无法读取提交的私钥。", "CHECK_PRIVATE_KEY_FORMAT"
	case "PRIVATE_KEY_IDENTITY_MISMATCH":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "私钥身份与所选公钥身份不匹配。", "SELECT_MATCHING_PUBLIC_KEY"
	case "AGENT_ENDPOINT_UNAVAILABLE":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "本机 SSH Agent 凭据不可用。", "CHECK_LOCAL_SSH_AGENT"
	case "RUN_WORKER_STOPPED":
		item.Category, item.Code = "INFRA_ERROR", code
		item.Message, item.Action = "任务执行器当前不可用。", "RESTART_SERVICE"
	case "OBSERVATION_UNVERIFIABLE":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "检查结果缺少可验证的观测证据。", "CHECK_EVIDENCE_SOURCE"
	case "SSH_HANDSHAKE_UNVERIFIABLE":
		item.Category, item.Code = "NETWORK_ERROR", code
		item.Message, item.Action = "SSH 握手未能完成，具体原因无法安全判定。", "CHECK_SSH_IDENTITY_AND_TRANSPORT"
	case "SSH_AUTH_REJECTED":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "SSH 服务端未接受所选身份验证方式。", "CHECK_ACCOUNT_AND_AUTHENTICATION_POLICY"
	case "SSH_CONNECTION_FAILED":
		item.Category, item.Code = "NETWORK_ERROR", code
		item.Message, item.Action = "无法建立到执行主机的 SSH 连接。", "CHECK_NETWORK_AND_SSH_SERVICE"
	case "SSH_HOST_IDENTITY_UNVERIFIABLE":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "无法验证执行主机的 SSH 身份。", "VERIFY_TRUSTED_HOST_KEY"
	case "REMOTE_EXECUTION_FAILED":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "远端检查步骤未能完成。", "CHECK_EXECUTION_REQUIREMENTS"
	case "REMOTE_COMMAND_TIMEOUT":
		item.Category, item.Code = "HOST_TIMEOUT", code
		item.Message, item.Action = "远端检查步骤超过时间限制。", "CHECK_HOST_LOAD_AND_RUN_BUDGET"
	case "HOST_TIMEOUT", "RUN_TIMEOUT":
		item.Category, item.Code = "HOST_TIMEOUT", code
		item.Message, item.Action = "任务超过配置的时间预算。", "REVIEW_RUN_BUDGET_AND_HOST_LOAD"
		item.Retryable = true
	case "IDENTITY_CONFLICT":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "多个目标报告了相同的机器身份。", "CHECK_INVENTORY_TARGETS"
	case "CANCEL_ACCEPTED":
		item.Category, item.Code = "RUN_CANCELLED", code
		item.Message, item.Action = "任务取消请求已持久接受。", "REVIEW_PARTIAL_RUN_RESULTS"
	case "NO_TARGETS":
		item.Category, item.Code = "PLAN_ERROR", code
		item.Message, item.Action = "任务没有可执行目标主机。", "CHECK_HOST_ROLE_BINDINGS"
	case "REMOTE_COMMAND_EXIT_UNEXPECTED":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "远端检查程序以非预期状态退出。", "CHECK_RUN_REQUIREMENTS"
	case "SUDO_RESULT_UNVERIFIABLE":
		item.Category, item.Code = "SUDO_UNVERIFIABLE", code
		item.Message, item.Action = "无法验证所需的 sudo 权限。", "CHECK_SUDO_POLICY"
	case "DNS_LOOKUP_FAILED":
		item.Category, item.Code = "NETWORK_ERROR", code
		item.Message, item.Action = "无法解析执行主机名称。", "CHECK_DNS_CONFIGURATION"
		item.Retryable = true
	case "TCP_CONNECT_REFUSED":
		item.Category, item.Code = "NETWORK_ERROR", code
		item.Message, item.Action = "执行主机拒绝了 SSH 连接。", "CHECK_SSH_SERVICE_AND_PORT"
	case "TCP_CONNECT_TIMEOUT":
		item.Category, item.Code = "NETWORK_ERROR", code
		item.Message, item.Action = "连接执行主机超过时间限制。", "CHECK_NETWORK_ROUTE_AND_FIREWALL"
		item.Retryable = true
	case "KNOWN_HOSTS_UNREADABLE":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "无法读取本机受信任的 SSH 主机密钥记录。", "CHECK_KNOWN_HOSTS_ACCESS"
	case "SSH_HOST_KEY_POLICY_INVALID":
		item.Category, item.Code = "PLAN_ERROR", code
		item.Message, item.Action = "SSH 主机密钥策略配置无效。", "CHECK_SSH_HOST_KEY_POLICY"
	case "HOST_KEY_UNKNOWN":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "目标 SSH 主机密钥尚未在受信任记录中登记。", "VERIFY_HOST_KEY_FROM_TRUSTED_SOURCE"
	case "HOST_KEY_CHANGED":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "目标 SSH 主机密钥与受信任记录不一致。", "VERIFY_HOST_KEY_CHANGE"
	case "HOST_KEY_REVOKED":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "目标 SSH 主机密钥已被标记为撤销。", "CONTACT_HOST_KEY_ADMINISTRATOR"
	case "SSH_NEGOTIATION_FAILED":
		item.Category, item.Code = "NETWORK_ERROR", code
		item.Message, item.Action = "SSH 双方没有可用的安全算法组合。", "CHECK_SSH_ALGORITHM_POLICY"
	case "AGENT_SELECTED_KEY_MISSING":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "指定的 SSH Agent 身份当前不可用。", "CHECK_SELECTED_AGENT_KEY"
	case "FACT_MACHINE_ID_MISSING", "FACT_BOOT_ID_INVALID", "CHECKSUM_TOOL_UNAVAILABLE", "REMOTE_FACTS_UNVERIFIABLE":
		item.Category, item.Code = "IDENTITY_ERROR", code
		item.Message, item.Action = "无法验证执行主机所需的身份事实。", "CHECK_REQUIRED_HOST_FACTS"
	case "SUDO_CREDENTIAL_MISSING":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "执行特权检查所需的 sudo 凭据不可用。", "PROVIDE_SUDO_CREDENTIAL"
	case "SUDO_POLICY_DENIED":
		item.Category, item.Code = "AUTH_ERROR", code
		item.Message, item.Action = "当前 sudo 策略拒绝了受控检查操作。", "CHECK_SUDOERS_POLICY"
	case "REMOTE_TEMP_CANDIDATES_EXHAUSTED":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "没有找到可安全执行检查的远端目录。", "CHECK_REMOTE_TEMP_PERMISSIONS"
	case "UPLOAD_FAILED":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "检查所需文件未能完整投递到目标主机。", "CHECK_REMOTE_WRITE_ACCESS"
	case "PAYLOAD_DIGEST_MISMATCH":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "目标主机上的检查文件未通过完整性验证。", "RETRY_AFTER_CHECKING_REMOTE_STORAGE"
	case "OUTPUT_LIMIT_EXCEEDED":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "远端检查结果超过允许的输出大小。", "REDUCE_CHECK_OUTPUT"
	case "RESULT_JSON_INVALID":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "远端检查结果无法解析为有效数据。", "CHECK_EXECUTOR_COMPATIBILITY"
	case "RESULT_RESOURCE_MISSING":
		item.Category, item.Code = "EXECUTION_ERROR", code
		item.Message, item.Action = "检查结果缺少预期资源。", "CHECK_CHECK_DEFINITION_AND_TARGET"
	case "PLAN_ERROR", "REQUEST_INVALID":
		item.Category, item.Code = "PLAN_ERROR", "PLAN_SCHEMA_INVALID"
		item.Message, item.Action = "提交内容未通过静态校验。", "CHECK_PLAN_AND_INVENTORY_FIELDS"
	case "PLAN_STATE_NOT_EXECUTABLE", "PLAN_RECORD_NOT_ACTIVE", "RELEASE_MANIFEST_UNVERIFIABLE":
		item.Category, item.Code = "PLAN_ERROR", code
		item.Message, item.Action = "当前方案状态或发布清单不允许执行。", "CHECK_PLAN_RELEASE_STATE"
	case "PARTIAL_SOURCE_RUN_UNAVAILABLE", "PARTIAL_INVENTORY_CHANGED", "PARTIAL_HOST_NOT_ELIGIBLE", "PARTIAL_HOSTS_REQUIRED":
		item.Category, item.Code = "PLAN_ERROR", code
		item.Message, item.Action = "局部复测范围与来源任务不匹配。", "CHECK_PARTIAL_RUN_SELECTION"
	case "RUN_MANAGER_UNAVAILABLE":
		item.Category, item.Code = "INFRA_ERROR", "RUN_WORKER_STOPPED"
		item.Message, item.Action = "任务执行器当前不可用。", "RESTART_SERVICE"
	default:
		item.Category, item.Code = "EXECUTION_ERROR", "DIAGNOSTIC_UNCLASSIFIED"
		item.Message, item.Action = "操作未能完成，具体原因无法安全判定。", "CHECK_RUN_DIAGNOSTICS"
	}
	return item
}
