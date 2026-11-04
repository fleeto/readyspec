"""Interactive Modal Screens for ReadySpec TUI (Template Picker, Shell Sandbox, Paste Import, Cleanup)."""

from __future__ import annotations

from pathlib import Path
from typing import Any

from textual.app import ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.screen import ModalScreen
from textual.widgets import (
    Button,
    Checkbox,
    Input,
    Label,
    Select,
    SelectionList,
    Static,
    TextArea,
)
from textual.widgets.selection_list import Selection

from readyspec.constants import (
    EvidenceDepth,
    PrivilegeGroup,
    TemplateMaturity,
    TemplateSource,
)
from readyspec.domain.models import (
    CleanupRecord,
    CustomShellSpec,
    NumericOperator,
    ParameterSpec,
    ParamType,
    PlanDocument,
    RoleSpec,
    ShellAssertionMode,
    ShellAssertionSpec,
    TemplateKind,
    TemplateSpec,
)
from readyspec.normalizer.shell_evaluator import evaluate_custom_shell_output
from readyspec.registry.builtin_catalog import search_templates
from readyspec.registry.workspace_store import PlanLifecycleError, WorkspaceStore


class HostTypeModal(ModalScreen[str | None]):
    """One-line prompt for a new host-type id."""

    BINDINGS = [Binding("escape", "cancel", "取消")]

    def compose(self) -> ComposeResult:
        with Vertical(classes="modal-compact"):
            yield Label("新建主机类型", classes="card-title")
            yield Input(placeholder="如 cache", id="ht-id")
            yield Static("", id="ht-err")
            with Horizontal(classes="toolbar-row"):
                yield Button("添加", id="ht-ok")
                yield Button("取消", id="ht-cancel")

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "ht-cancel":
            self.dismiss(None)
            return
        if event.button.id != "ht-ok":
            return
        rid = self.query_one("#ht-id", Input).value.strip()
        if not rid:
            self.query_one("#ht-err", Static).update("请填写主机类型标识")
            return
        self.dismiss(rid)


class NewPlanModal(ModalScreen[PlanDocument | None]):
    """Create a new editable Draft Plan."""

    BINDINGS = [Binding("escape", "cancel", "取消")]

    def compose(self) -> ComposeResult:
        with Vertical(classes="modal-compact"):
            yield Label("新建检查方案（草稿）", classes="card-title")
            yield Label("方案标识 ID（仅限字母/数字/.-_）：")
            yield Input(value="my-service-check", id="np-id")
            yield Label("方案中文名称：")
            yield Input(value="核心业务服务部署前检查", id="np-name")
            yield Label("适用软件名称与版本：")
            yield Input(value="MyService 1.0", id="np-sw")
            yield Label("首个主机类型标识（如 app / web / db）：")
            yield Input(value="app", id="np-role")
            yield Static("", id="np-err")
            with Horizontal(classes="toolbar-row"):
                yield Button("创建草稿", id="np-ok")
                yield Button("取消", id="np-cancel")

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "np-cancel":
            self.dismiss(None)
            return
        if event.button.id == "np-ok":
            pid = self.query_one("#np-id", Input).value.strip()
            name = self.query_one("#np-name", Input).value.strip()
            sw = self.query_one("#np-sw", Input).value.strip()
            rid = self.query_one("#np-role", Input).value.strip() or "app"
            if not pid or not name:
                self.query_one("#np-err", Static).update("[red]方案 ID 与名称不能为空[/red]")
                return
            plan = PlanDocument(
                plan_id=pid,
                name=name,
                software_name=sw,
                version="0.1.0",
                roles=[RoleSpec(role_id=rid, name=f"{rid} 节点", min_nodes=1)],
            )
            self.dismiss(plan)


class PasteImportModal(ModalScreen[PlanDocument | None]):
    """Import a Plan via direct YAML paste or file path without SSH/Shell execution (CHG-002)."""

    BINDINGS = [Binding("escape", "cancel", "关闭")]

    def __init__(self, store: WorkspaceStore) -> None:
        super().__init__()
        self._store = store

    def compose(self) -> ComposeResult:
        with Vertical(classes="modal-dialog"):
            yield Label("导入外部检查方案（支持文件路径或直接粘贴自包含 YAML）", classes="card-title")
            with Horizontal(classes="toolbar-row"):
                yield Input(placeholder="可选：输入本地 YAML 文件路径后点『读取文件』...", id="imp-path")
                yield Button("读取文件", id="imp-load-file")
            yield Label("或直接将方案 YAML 文本粘贴到下方（安全解析，禁止重复键，不执行任何命令）：")
            yield TextArea("", language="yaml", id="paste-yaml-area")
            yield Static("", id="imp-status")
            with Horizontal(classes="toolbar-row"):
                yield Button("安全校验并导入工作区", id="imp-submit")
                yield Button("取消", id="imp-cancel")

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        status = self.query_one("#imp-status", Static)
        area = self.query_one("#paste-yaml-area", TextArea)
        if event.button.id == "imp-cancel":
            self.dismiss(None)
        elif event.button.id == "imp-load-file":
            p_str = self.query_one("#imp-path", Input).value.strip()
            if not p_str:
                status.update("[yellow]请先输入文件路径[/yellow]")
                return
            p = Path(p_str).expanduser()
            if not p.exists() or not p.is_file():
                status.update(f"[red]找不到文件: {p}[/red]")
                return
            area.text = p.read_text(encoding="utf-8", errors="replace")
            status.update(f"[green]已载入文件 {p.name}，请点击『安全校验并导入工作区』[/green]")
        elif event.button.id == "imp-submit":
            yaml_text = area.text.strip()
            if not yaml_text:
                status.update("[red]YAML 内容为空[/red]")
                return
            try:
                imported = self._store.import_plan_yaml(yaml_text, save_to_workspace=True)
                self.dismiss(imported)
            except PlanLifecycleError as exc:
                status.update(f"[red][{exc.code}] {exc.message}[/red]")


class TemplatePickerModal(ModalScreen[list[TemplateSpec] | None]):
    """Act 2 `[a]`: Dual-pane bilingual search + Space multi-select template picker."""

    BINDINGS = [Binding("escape", "cancel", "取消")]

    def __init__(self, templates: list[TemplateSpec], role_name: str) -> None:
        super().__init__()
        self._all_templates = [
            t for t in templates if t.kind != TemplateKind.CONTROL and t.maturity == TemplateMaturity.INTEGRATED
        ]
        self._by_id = {t.template_id: t for t in self._all_templates}
        self._role_name = role_name
        self._selected_ids: set[str] = set()

    def compose(self) -> ComposeResult:
        with Vertical(classes="modal-dialog"):
            yield Label(
                f"向角色『{self._role_name}』添加内置/公共检查模板（支持中英文搜索 + 空格多选）",
                classes="card-title",
            )
            yield Input(
                placeholder="输入关键词过滤（如：内存 / memory / 端口 / port / sshd / elf / oom）...",
                id="picker-search",
            )
            with Horizontal(id="picker-split"):
                with Vertical(id="picker-left"):
                    yield SelectionList[str](id="picker-list")
                with VerticalScroll(id="picker-right"):
                    yield Static("在左侧上下移动高亮即可预览模板契约详情", id="picker-detail")
            with Horizontal(classes="toolbar-row"):
                yield Button("将勾选项批量加入当前角色", id="picker-confirm")
                yield Button("取消", id="picker-cancel")

    def on_mount(self) -> None:
        self._refresh_list("")
        self.query_one("#picker-search", Input).focus()

    def _refresh_list(self, query: str) -> None:
        sel_list = self.query_one("#picker-list", SelectionList)
        self._selected_ids.update(sel_list.selected)
        filtered = search_templates(self._all_templates, query)
        sel_list.clear_options()
        for tpl in filtered:
            priv_tag = " [PRIV]" if tpl.privilege_group == PrivilegeGroup.PRIVILEGED else ""
            label = f"[{tpl.template_id}] {tpl.name_zh} ({tpl.name_en}){priv_tag}"
            sel_list.add_option(Selection(label, tpl.template_id, tpl.template_id in self._selected_ids))
        if filtered:
            self._render_detail(filtered[0])

    def _render_detail(self, tpl: TemplateSpec) -> None:
        params_desc = "\n".join(
            f"  • {p.name} ({p.param_type.value}): {p.label_zh} [默认={p.default!r}]"
            + (" [默认建议现场可覆盖]" if p.overridable_by_default else "")
            for p in tpl.parameters
        ) or "  （无额外输入参数）"
        deps_str = ", ".join(tpl.dependencies) if tpl.dependencies else "Goss 原生资源（无外部命令依赖）"
        text = (
            f"[bold]{tpl.template_id} — {tpl.name_zh}[/bold]\n"
            f"英文名: {tpl.name_en}\n"
            f"来源/成熟度: {tpl.source.value} / {tpl.maturity.value}\n"
            f"执行权限组: {tpl.privilege_group.value}\n"
            f"最低证据深度: {tpl.evidence_depth.value}\n"
            f"目标端依赖: {deps_str}\n"
            f"{('条件说明: ' + tpl.conditional_notes + chr(10)) if tpl.conditional_notes else ''}\n"
            f"{tpl.description}\n\n"
            f"[bold]参数契约:[/bold]\n{params_desc}"
        )
        self.query_one("#picker-detail", Static).update(text)

    def on_input_changed(self, event: Input.Changed) -> None:
        if event.input.id == "picker-search":
            self._refresh_list(event.value)

    def on_selection_list_selection_highlighted(
        self, event: SelectionList.SelectionHighlighted[str]
    ) -> None:
        tid = str(event.selection.value)
        if tid in self._by_id:
            self._render_detail(self._by_id[tid])

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "picker-cancel":
            self.dismiss(None)
        elif event.button.id == "picker-confirm":
            sel_list = self.query_one("#picker-list", SelectionList)
            self._selected_ids.update(sel_list.selected)
            if not self._selected_ids and sel_list.highlighted is not None:
                opt = sel_list.get_option_at_index(sel_list.highlighted)
                self._selected_ids.add(str(opt.value))
            chosen = [self._by_id[tid] for tid in sorted(self._selected_ids) if tid in self._by_id]
            self.dismiss(chosen)


class CustomShellModal(ModalScreen[tuple[TemplateSpec, bool] | None]):
    """Act 2 `[c]`: Plan-local Custom Shell Editor + `$RS_PARAM_*` env + Live Offline Sandbox."""

    BINDINGS = [Binding("escape", "cancel", "取消")]

    def __init__(self, existing: TemplateSpec | None = None) -> None:
        super().__init__()
        self._existing = existing

    def compose(self) -> ComposeResult:
        tid_default = self._existing.template_id if self._existing else "local.custom_check"
        zh_default = self._existing.name_zh if self._existing else "自定义环境检查脚本"
        param_name = (
            self._existing.parameters[0].name
            if (self._existing and self._existing.parameters)
            else "target_dir"
        )
        param_def = (
            str(self._existing.parameters[0].default or "/data")
            if (self._existing and self._existing.parameters)
            else "/data"
        )
        script_def = (
            self._existing.shell_spec.script
            if (self._existing and self._existing.shell_spec)
            else 'test -d "$RS_PARAM_TARGET_DIR" && echo "dir=$RS_PARAM_TARGET_DIR status=ok"'
        )
        contains_def = (
            (self._existing.shell_spec.assertions.stdout_contains or "")
            if (self._existing and self._existing.shell_spec)
            else "status=ok"
        )

        with VerticalScroll(classes="modal-dialog"):
            yield Label(
                "自定义 Shell 检查编辑器（STANDARD 非特权 · 参数经只读环境变量 $RS_PARAM_<NAME> 安全注入）",
                classes="card-title",
            )
            with Horizontal(classes="toolbar-row"):
                yield Input(value=tid_default, placeholder="模板 ID (如 local.app_check)", id="cs-id")
                yield Input(value=zh_default, placeholder="中文标题", id="cs-zh")
            with Horizontal(classes="toolbar-row"):
                yield Input(value=param_name, placeholder="参数名 (自动映射为 $RS_PARAM_大写)", id="cs-pname")
                yield Input(value=param_def, placeholder="参数默认值", id="cs-pdef")
            yield Label("POSIX /bin/sh 多行脚本（严禁通过非 root 连接 sudo 提权）：")
            yield TextArea(script_def, language="bash", id="shell-script-area")
            with Horizontal(classes="toolbar-row"):
                yield Input(value="0", placeholder="期望退出码", id="cs-exit")
                yield Input(value=contains_def, placeholder="期望 stdout 包含子串 (可留空)", id="cs-contains")
                yield Input(value="", placeholder="可选单数值 >= 阈值 (如 1024)", id="cs-num-ge")
            yield Checkbox("保存时同步提升一份到工作区『用户公共模板库』(REQ-TPL-15)", id="cs-promote")

            yield Label("── 离线断言模拟自测沙盘（不连服务器、不运行真实 Shell，Section 7.2）──", classes="col-header")
            with Horizontal(classes="toolbar-row"):
                yield Input(value="0", placeholder="模拟退出码", id="cs-sim-exit")
                yield Input(value="dir=/data status=ok", placeholder="模拟 stdout 输出", id="cs-sim-out")
                yield Button("离线自测断言", id="cs-test-btn")
            yield Static("点击『离线自测断言』立即验证判定器结论", id="cs-test-result")

            with Horizontal(classes="toolbar-row"):
                yield Button("保存并加入当前角色", id="cs-save")
                yield Button("取消", id="cs-cancel")

    def _build_assertions(self) -> ShellAssertionSpec:
        try:
            exp_exit = int(self.query_one("#cs-exit", Input).value.strip() or "0")
        except ValueError:
            exp_exit = 0
        contains = self.query_one("#cs-contains", Input).value.strip() or None
        num_str = self.query_one("#cs-num-ge", Input).value.strip()
        num_op = NumericOperator.GE if num_str else None
        num_thr = float(num_str) if num_str else None
        return ShellAssertionSpec(
            mode=ShellAssertionMode.BASIC,
            expected_exit_code=exp_exit,
            stdout_contains=contains,
            numeric_op=num_op,
            numeric_threshold=num_thr,
        )

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "cs-cancel":
            self.dismiss(None)
            return
        if event.button.id == "cs-test-btn":
            try:
                sim_exit = int(self.query_one("#cs-sim-exit", Input).value.strip() or "0")
            except ValueError:
                sim_exit = 1
            sim_out = self.query_one("#cs-sim-out", Input).value
            outcome = evaluate_custom_shell_output(
                self._build_assertions(),
                exit_code=sim_exit,
                stdout=sim_out,
            )
            color = "green" if outcome.status.value == "PASS" else ("red" if outcome.status.value == "FAIL" else "yellow")
            self.query_one("#cs-test-result", Static).update(
                f"[{color}]离线自测结论: {outcome.status.value} — {outcome.summary} (期望: {outcome.expected})[/{color}]"
            )
            return
        if event.button.id == "cs-save":
            tid = self.query_one("#cs-id", Input).value.strip() or "local.custom_check"
            if not tid.startswith("local.") and not tid.startswith("user."):
                tid = f"local.{tid}"
            zh = self.query_one("#cs-zh", Input).value.strip() or "自定义 Shell 检查"
            p_name = self.query_one("#cs-pname", Input).value.strip()
            p_def = self.query_one("#cs-pdef", Input).value.strip()
            script = self.query_one("#shell-script-area", TextArea).text.strip() or "true"
            params: list[ParameterSpec] = []
            if p_name:
                params.append(
                    ParameterSpec(
                        name=p_name,
                        label_zh=f"参数 {p_name} ($RS_PARAM_{p_name.upper()})",
                        param_type=ParamType.STRING,
                        default=p_def,
                        overridable_by_default=True,
                    )
                )
            tpl = TemplateSpec(
                template_id=tid,
                version="1.0.0",
                name_zh=zh,
                name_en=tid,
                domain_code="D",
                group_code="CUSTOM",
                description="方案内自定义 POSIX Shell 检查（参数通过只读环境变量安全传递）。",
                source=TemplateSource.PLAN_LOCAL,
                maturity=TemplateMaturity.INTEGRATED,
                privilege_group=PrivilegeGroup.STANDARD,
                evidence_depth=EvidenceDepth.CONFIGURATION,
                kind=TemplateKind.CUSTOM_SHELL,
                parameters=params,
                shell_spec=CustomShellSpec(
                    interpreter="/bin/sh",
                    script=script,
                    assertions=self._build_assertions(),
                ),
            ).with_digest()
            promote = self.query_one("#cs-promote", Checkbox).value
            self.dismiss((tpl, promote))


class CloneToRoleModal(ModalScreen[str | None]):
    """Act 2 `[d]`: Clone selected CheckInstance into a target role with a new independent ID."""

    BINDINGS = [Binding("escape", "cancel", "取消")]

    def __init__(self, roles: list[RoleSpec], instance_title: str) -> None:
        super().__init__()
        self._roles = roles
        self._title = instance_title

    def compose(self) -> ComposeResult:
        options = [(f"{r.name} ({r.role_id})", r.role_id) for r in self._roles]
        with Vertical(classes="modal-compact"):
            yield Label(f"复制实例『{self._title}』为新独立实例 (REQ-TPL-08)", classes="card-title")
            yield Label("选择目标业务角色（复制后拥有全新稳定 ID，修改互不串改）：")
            yield Select(options, value=options[0][1] if options else Select.BLANK, id="clone-role-sel")
            with Horizontal(classes="toolbar-row"):
                yield Button("确认复制", id="clone-ok")
                yield Button("取消", id="clone-cancel")

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "clone-cancel":
            self.dismiss(None)
        elif event.button.id == "clone-ok":
            val = self.query_one("#clone-role-sel", Select).value
            self.dismiss(str(val) if val != Select.BLANK else None)


class YamlViewerModal(ModalScreen[None]):
    """Read-only modal displaying exported self-contained Plan YAML or compiled Goss YAML."""

    BINDINGS = [Binding("escape", "close", "关闭")]

    def __init__(self, title: str, yaml_text: str, saved_hint: str = "") -> None:
        super().__init__()
        self._title = title
        self._yaml_text = yaml_text
        self._saved_hint = saved_hint

    def compose(self) -> ComposeResult:
        with Vertical(classes="modal-dialog"):
            yield Label(self._title, classes="card-title")
            if self._saved_hint:
                yield Static(f"[green]{self._saved_hint}[/green]")
            yield TextArea(self._yaml_text, language="yaml", read_only=True, id="yaml-viewer-area")
            with Horizontal(classes="toolbar-row"):
                yield Button("关闭 [Esc]", id="yv-close")

    def action_close(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "yv-close":
            self.dismiss(None)


class CleanupActionModal(ModalScreen[dict[str, Any] | None]):
    """Act 4 Cleanup Wizard modal enforcing triple-fingerprint & live-PID confirmation (TODO-007)."""

    BINDINGS = [Binding("escape", "cancel", "关闭")]

    def __init__(self, record: CleanupRecord) -> None:
        super().__init__()
        self._rec = record

    def compose(self) -> ComposeResult:
        r = self._rec
        with Vertical(classes="modal-compact"):
            yield Label(f"受控残留清理处置 — {r.cleanup_id}", classes="card-title")
            yield Static(
                f"目标主机: [bold]{r.host_name}[/bold] ({r.privilege_group.value})\n"
                f"受控路径: {r.remote_dir}\n"
                f"归属指纹: token={r.owner_token[:18]}... | boot_id={r.boot_id}\n"
                + (
                    f"[bold red]⚠ 检测到远端存活进程 PID={r.process_pid} (starttime={r.process_starttime_ticks}):\n"
                    f"  {r.live_process_cmdline}[/bold red]"
                    if r.live_process_detected
                    else "[green]探活结果：无残留存活进程，可直接安全清理目录[/green]"
                )
            )
            if r.live_process_detected:
                yield Checkbox(
                    f"我已知悉并确认向存活进程 PID={r.process_pid} 发送 SIGTERM 终止后删除目录 (TODO-007)",
                    id="cln-confirm-kill",
                )
            yield Label("或填写人工接管原因标记为 WAIVED（保留永久未清理审计记录，REQ-RUN-13）：")
            yield Input(placeholder="若选择标记 WAIVED，请在此填写原因...", id="cln-waive-reason")
            yield Static("", id="cln-err")
            with Horizontal(classes="toolbar-row"):
                yield Button("执行受控清理", id="cln-do-clean")
                yield Button("标记为 WAIVED", id="cln-do-waive")
                yield Button("取消", id="cln-cancel")

    def action_cancel(self) -> None:
        self.dismiss(None)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        err = self.query_one("#cln-err", Static)
        if event.button.id == "cln-cancel":
            self.dismiss(None)
        elif event.button.id == "cln-do-clean":
            confirm_kill = False
            if self._rec.live_process_detected:
                confirm_kill = self.query_one("#cln-confirm-kill", Checkbox).value
                if not confirm_kill:
                    err.update("[red]存在远端存活进程，必须先勾选上方确认框才允许终止进程并清理！[/red]")
                    return
            self.dismiss({"mode": "clean", "confirm_kill": confirm_kill})
        elif event.button.id == "cln-do-waive":
            reason = self.query_one("#cln-waive-reason", Input).value.strip()
            if not reason:
                err.update("[red]标记 WAIVED 必须填写非空原因（留存安全审计）！[/red]")
                return
            self.dismiss({"mode": "waive", "reason": reason})
