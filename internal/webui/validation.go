package webui

import (
	"errors"
	"github.com/fleeto/readyspec/internal/diagnostic"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/publicview"
	"net/http"
)

func parameterDiagnostic(err error) diagnostic.Item {
	var issue *plan.ParameterError
	if !errors.As(err, &issue) {
		return diagnostic.Item{}
	}
	messages := map[string]string{
		"REQUIRED": "请填写必填参数。", "CAPACITY_FORMAT": "容量需要数字和单位，例如 4 GiB 或 100 GB；单位区分大小写。",
		"CAPACITY_RANGE": "容量超出支持范围、不是完整字节或低于允许下限。", "PARAMETER_CONSTRAINT": "参数不满足模板类型或条件约束。",
		"UNKNOWN_HOST":      "角色绑定引用了清单中不存在的主机，请核对主机清单和绑定标识。",
		"SSH_USER_REQUIRED": "目标主机缺少 SSH 登录用户名，请在 Ansible INI/YAML 中填写 ansible_user。",
		"PARAMETER_FORMAT":  "参数格式不符合要求。", "PARAMETER_RANGE": "参数范围上下界不符合要求。",
	}
	message := messages[issue.Code]
	if message == "" {
		message = "参数未通过校验。"
	}
	return diagnostic.Item{Category: "PLAN_ERROR", Phase: "SUBMISSION", Code: issue.Code, Field: issue.Field, RoleID: issue.RoleID, ItemID: issue.ItemID, Message: message, Action: "CHECK_PLAN_PARAMETERS"}
}

func writeValidationError(w http.ResponseWriter, err error) {
	item := parameterDiagnostic(err)
	if item.Code == "" {
		writeError(w, http.StatusUnprocessableEntity, "PLAN_ERROR: 内容未通过静态校验")
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "diagnostics": []diagnostic.Item{item}})
}

func (s *Server) getRunDisplayMetadata(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	run, err := s.runs.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND")
		return
	}
	writeJSON(w, http.StatusOK, publicview.DisplayMetadataValue(*run))
}
