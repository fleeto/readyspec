"""ReadySpec V1 4-Act Interactive Textual TUI Application."""

from __future__ import annotations

import json
import uuid
from collections.abc import Callable
from typing import Any

from textual import events, work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.widgets import (
    Button,
    Checkbox,
    ContentSwitcher,
    DataTable,
    Input,
    Label,
    Select,
    Static,
    TextArea,
)

from readyspec.constants import (
    CheckStatus,
    HostPhase,
    Importance,
    PlanState,
    PrivilegeGroup,
    ReadinessStatus,
    RunScope,
)
from readyspec.domain.models import (
    CheckInstance,
    CheckInstanceResult,
    InventorySnapshot,
    ParameterOverrideRule,
    ParameterSpec,
    ParamType,
    PlanDocument,
    RoleSpec,
    RunBinding,
    RunReport,
    TemplateKind,
    TemplateSpec,
)
from readyspec.execution.mock_executor import SIMULATION_PROFILES
from readyspec.inventory.parser import (
    InventoryParseError,
    auto_suggest_role_mapping,
    parse_inventory_auto,
)
from readyspec.registry.builtin_catalog import get_catalog_groups
from readyspec.registry.workspace_store import PlanLifecycleError, WorkspaceStore
from readyspec.service.controller import ControllerSecurityError, RunController
from readyspec.tui.modals import (
    CleanupActionModal,
    CloneToRoleModal,
    CustomShellModal,
    HostTypeModal,
    NewPlanModal,
    PasteImportModal,
    TemplatePickerModal,
    YamlViewerModal,
)

DEFAULT_SAMPLE_INI = """[web]
web-node-01 ansible_host=10.20.1.11 ansible_user=ops
web-node-02 ansible_host=10.20.1.12 ansible_user=ops

[db]
db-node-01 ansible_host=10.20.2.21 ansible_user=ops
"""


def _bump_patch_version(ver: str) -> str:
    parts = ver.split(".")
    if len(parts) == 3 and parts[2].isdigit():
        return f"{parts[0]}.{parts[1]}.{int(parts[2]) + 1}"
    return f"{ver}.1"


class ReadySpecApp(App[None]):
    """4-Act Interactive TUI: [1 首页] [2 方案工作台] [3 运行与报告] [4 模板库与系统]."""

    CSS_PATH = "readyspec.tcss"
    TITLE = "ReadySpec V1"
    # Textual toasts have no parent; mouse text-selection on them crashes.
    ALLOW_SELECT = False

    BINDINGS = [
        Binding("f1", "nav('view-home')", "1 首页", show=False),
        Binding("f2", "nav('view-workbench')", "2 方案工作台", show=False),
        Binding("f3", "nav('view-run-report')", "3 运行与报告", show=False),
        Binding("f4", "nav('view-system')", "4 模板库与系统", show=False),
        Binding("f5", "launch_wizard_run", "开始执行", show=False),
        Binding("f6", "cycle_narrow_pane", "切栏(窄屏)", show=False),
        Binding("f9", "jump_cleanup", "待清理", show=False),
        Binding("ctrl+s", "save_draft", "保存草稿", show=False),
        Binding("q", "quit_if_nav", "退出", show=False),
    ]

    def __init__(
        self,
        store: WorkspaceStore | None = None,
        controller: RunController | None = None,
    ) -> None:
        super().__init__()
        self.store = store or WorkspaceStore(seed_demo=True)
        self.controller = controller or RunController(self.store)

        plans = self.store.list_plans()
        drafts = [p for p in plans if p.state == PlanState.DRAFT]
        self.current_plan: PlanDocument = (drafts[0] if drafts else plans[0]).model_copy(deep=True)
        self.selected_role_id: str = (
            self.current_plan.roles[0].role_id if self.current_plan.roles else ""
        )
        self.selected_instance_id: str = (
            self.current_plan.roles[0].instances[0].instance_id
            if (self.current_plan.roles and self.current_plan.roles[0].instances)
            else ""
        )
        self._undo_deleted_stack: list[tuple[str, int, CheckInstance]] = []
        self._narrow_pane_idx: int = 1  # 0=roles, 1=instances, 2=params

        # Run Wizard & Report state
        released_plans = [p for p in plans if p.state == PlanState.RELEASED]
        self.wizard_plan: PlanDocument = (
            released_plans[0] if released_plans else self.current_plan
        )
        self.wizard_inv_mode: str = "ansible_ini"  # quick_ip | ansible_ini
        self.last_binding: RunBinding | None = None
        reports = self.store.list_run_reports()
        self.current_report: RunReport | None = reports[0] if reports else None
        self.report_perspective: str = "host"  # host | check
        self.report_only_anomalies: bool = False
        self._report_row_evidence_map: dict[str, str] = {}
        self._live_host_phases: dict[str, tuple[HostPhase, str]] = {}
        self._wb_title_input: Input | None = None
        self._wb_importance_select: Select[Any] | None = None
        self._wb_param_widgets: dict[
            str, tuple[Input | Select[Any] | list[tuple[str, Checkbox]], Checkbox]
        ] = {}
        self._wiz_role_inputs: dict[str, Input] = {}
        self._wiz_ov_inputs: dict[tuple[str, str, str], Input] = {}
        self._wb_dirty = False
        self._wb_loading = False
        self._wb_pending: Callable[[], None] | None = None

    def _is_text_editing(self) -> bool:
        focused = self.focused
        return isinstance(focused, (Input, TextArea))

    def compose(self) -> ComposeResult:
        # Top Navigation Bar (4 top-level views + simulation badge + pending cleanup jump)
        with Horizontal(id="top-bar"):
            yield Label("◆ ReadySpec", id="brand-label")
            yield Button("1 首页", id="nav-view-home", classes="nav-btn nav-active")
            yield Button("2 方案工作台", id="nav-view-workbench", classes="nav-btn")
            yield Button("3 运行与报告", id="nav-view-run-report", classes="nav-btn")
            yield Button("4 模板库与系统", id="nav-view-system", classes="nav-btn")
            yield Static("", id="top-spacer")
            yield Label("[原型演示 · 仿真远端]", id="sim-badge")
            yield Button("清理: 0", id="cleanup-badge")

        with ContentSwitcher(initial="view-home", id="main-switcher"):
            # ==========================================================
            # ACT 1: Dual-Card Home Screen
            # ==========================================================
            with Vertical(id="view-home"):
                with Horizontal(id="home-cards-row"):
                    with Vertical(classes="role-entry-card"):
                        yield Label("🚀 部署 / 运维工程师入口", classes="card-title")
                        yield Static(
                            "无需理解底层 Goss 语法：加载或粘贴已发布方案 → 填入主机 IP 或兼容 Ansible 清单 → 静态预览确认 → 一键执行检查并定位问题。"
                        )
                        with Horizontal(classes="card-btn-row"):
                            yield Button("立即开始检查", id="home-start-run")
                            yield Button("粘贴/导入方案", id="home-import-plan")
                            yield Button("重跑上次检查", id="home-rerun-last")

                    with Vertical(classes="role-entry-card"):
                        yield Label("🛠 业务开发人员入口", classes="card-title")
                        yield Static(
                            "按主机类型声明部署前置条件：40 项 P0 模板即搜即加、编写安全参数化自定义 Shell、草稿试跑、一键导出自包含单文件 YAML。"
                        )
                        with Horizontal(classes="card-btn-row"):
                            yield Button("新建检查方案", id="home-new-plan")
                            yield Button("进入方案工作台", id="home-open-wb")
                            yield Button("浏览 54 组模板库", id="home-browse-tpl")

                with Vertical(id="home-recent-box"):
                    yield Label("最近检查运行记录（选中行按 Enter 查看完整双视角报告）", classes="col-header")
                    yield DataTable(id="home-runs-table", cursor_type="row")

            # ==========================================================
            # ACT 2: Responsive 3-Column Plan Workbench
            # ==========================================================
            with Vertical(id="view-workbench"):
                with Horizontal(id="wb-header"):
                    yield Select([], id="wb-plan-select")
                    yield Static("", id="wb-plan-summary")

                yield Static("", id="wb-diag-bar")

                with Horizontal(id="wb-columns"):
                    with Vertical(id="wb-col-roles", classes="wb-col"):
                        yield Label("主机类型", classes="col-header")
                        yield DataTable(id="wb-roles-table", cursor_type="row")
                        yield Static("", id="wb-compat-info")

                    with Vertical(id="wb-col-instances", classes="wb-col"):
                        yield Label("检查实例", classes="col-header")
                        yield DataTable(id="wb-inst-table", cursor_type="row")

                    with VerticalScroll(id="wb-col-params", classes="wb-col"):
                        yield Label("参数", classes="col-header")
                        yield Static("请选择一条检查实例", id="wb-param-meta")
                        yield Vertical(id="wb-param-fields")

            # ==========================================================
            # ACT 3 & ACT 4A: 4-Step Run Wizard + Live 9-Phase Board + Dual Report
            # ==========================================================
            with Vertical(id="view-run-report"):
                with Horizontal(id="rr-subbar"):
                    yield Button("①~④ 四步执行向导", id="rr-tab-wizard", classes="is-selected")
                    yield Button("实时看板", id="rr-tab-live")
                    yield Button("检查结果报告", id="rr-tab-report")
                    yield Select([], id="rr-history-select", prompt="切换历史报告...")

                with ContentSwitcher(initial="rr-pane-wizard", id="rr-switcher"):
                    # 4-Step Wizard
                    with VerticalScroll(id="rr-pane-wizard"):
                        with Vertical(classes="step-box"):
                            yield Label("步骤 ①：选择已发布方案（或粘贴外部方案 YAML）", classes="card-title")
                            with Horizontal(classes="toolbar-row"):
                                yield Select([], id="wiz-plan-select")
                                yield Button("粘贴导入外部方案", id="wiz-paste-plan")
                            yield Static("", id="wiz-plan-info")

                        with Vertical(classes="step-box"):
                            yield Label("步骤 ②：目标主机清单与短期内存凭据（终态即焚，绝不落盘）", classes="card-title")
                            with Horizontal(classes="toolbar-row"):
                                yield Button("兼容 Ansible Inventory (INI/YAML)", id="wiz-mode-ini", classes="is-selected")
                                yield Button("快速填 IP 列表", id="wiz-mode-ip")
                                yield Input(value="ops", placeholder="默认 SSH 用户", id="wiz-ssh-user")
                                yield Input(placeholder="SSH/Sudo 密码（无回显纯内存托管）", password=True, id="wiz-secret")
                            yield TextArea(DEFAULT_SAMPLE_INI, id="inv-textarea")
                            with Horizontal(classes="toolbar-row"):
                                yield Label("仿真远端环境画像：")
                                yield Select(
                                    [(desc, key) for key, desc in SIMULATION_PROFILES.items()],
                                    value="mixed_realistic",
                                    allow_blank=False,
                                    id="wiz-sim-profile",
                                )

                        with Vertical(classes="step-box"):
                            yield Label("步骤 ③：角色映射与允许现场调整的参数覆盖", classes="card-title")
                            with Horizontal(classes="toolbar-row"):
                                yield Button("解析主机清单并自动匹配角色", id="wiz-parse-btn")
                            yield Vertical(id="wiz-role-map-box")
                            yield Vertical(id="wiz-overrides-box")

                        with Vertical(classes="step-box"):
                            yield Label("步骤 ④：单屏静态预览与特权 / root 风险确认单（无 SSH）", classes="card-title")
                            yield Static("点击『刷新静态预览』查看预期主机数、检查项数及特权需求", id="wiz-preview-text")
                            yield Checkbox("批准执行方案内的受信内置特权检查（如 A03 sshd -T）", value=True, id="wiz-approve-priv")
                            yield Checkbox("显式批准使用 root SSH 连接身份运行（高风险强审计，CHG-001）", value=False, id="wiz-approve-root")
                            with Horizontal(classes="toolbar-row"):
                                yield Button("刷新静态预览", id="wiz-preview-btn")
                                yield Button("▶ 确认无误，立即开始执行检查", id="wiz-launch-btn")

                    # Live 9-Phase Progress Board
                    with Vertical(id="rr-pane-live"):
                        with Vertical(id="live-board-box"):
                            yield Label("远端 9 阶段实时生命周期看板（QUEUED → ... → FINISHED）", classes="card-title")
                            yield Static("当前无活动运行", id="live-run-banner")
                            yield DataTable(id="live-phase-table", cursor_type="row")
                            with Horizontal(classes="toolbar-row"):
                                yield Button("协作式取消当前运行", id="live-cancel-btn")
                                yield Button("查看已完成报告", id="live-goto-report")

                    # Dual-Perspective Report View
                    with Vertical(id="rr-pane-report"):
                        with Vertical(id="report-container"):
                            yield Static("暂无报告", id="report-banner")
                            with Horizontal(classes="toolbar-row"):
                                yield Button("按主机视角", id="rep-view-host", classes="is-selected")
                                yield Button("按检查项视角", id="rep-view-check")
                                yield Button("[f]仅看异常: 关", id="rep-toggle-anomaly")
                                yield Button("[g]查看编译Goss工件", id="rep-view-goss")
                                yield Button("导出JSON报告", id="rep-export-json")
                                yield Button("[r]全量正式复测", id="rep-rerun-full")
                                yield Button("[t]仅快测异常主机(PARTIAL)", id="rep-rerun-partial")

                            with Horizontal(id="report-main-split"):
                                with Vertical(id="report-table-pane"):
                                    yield DataTable(id="report-table", cursor_type="row")
                                with VerticalScroll(id="report-evidence-pane"):
                                    yield Static("选中左侧条目查看 Expected / Observed 证据链与修复建议", id="report-evidence-detail")

            # ==========================================================
            # ACT 4B: 54-Group Catalog Browser & Crash Cleanup Wizard
            # ==========================================================
            with Vertical(id="view-system"):
                with Horizontal(id="sys-split"):
                    with Vertical(id="sys-catalog-pane"):
                        yield Label("九大领域 · 54 个检查组全量目录（40 项 Integrated P0 + 14 项 Planned）", classes="col-header")
                        yield Input(placeholder="中英文筛选目录（如：硬件 / 存储 / 网络 / P01 / PLANNED）...", id="sys-cat-search")
                        yield DataTable(id="sys-cat-table", cursor_type="row")

                    with Vertical(id="sys-cleanup-pane"):
                        yield Label("受控临时目录与崩溃恢复待办（三重归属指纹验证，TODO-007）", classes="col-header")
                        with Horizontal(classes="toolbar-row"):
                            yield Button("处置选中待清理项 [Enter]", id="sys-resolve-btn")
                            yield Button("刷新列表", id="sys-refresh-cleanups")
                        yield DataTable(id="sys-cleanup-table", cursor_type="row")
                        yield Static("", id="sys-cleanup-detail")


    # ------------------------------------------------------------------
    # Lifecycle & Responsive Layout (140 / 100 / 80 columns, REQ-TUI-03)
    # ------------------------------------------------------------------
    def on_mount(self) -> None:
        self._setup_tables()
        self._refresh_all_views()
        self._apply_responsive_layout(self.size.width)

    def on_resize(self, event: events.Resize) -> None:
        self._apply_responsive_layout(event.size.width)

    def _apply_responsive_layout(self, width: int) -> None:
        try:
            col_roles = self.query_one("#wb-col-roles")
            col_inst = self.query_one("#wb-col-instances")
            col_params = self.query_one("#wb-col-params")
        except Exception:
            return

        for col in (col_roles, col_inst, col_params):
            col.remove_class("hidden-pane", "full-pane", "half-pane")

        if width >= 140:
            return
        if width >= 100:
            if self._narrow_pane_idx == 2:
                col_roles.add_class("hidden-pane")
                col_inst.add_class("half-pane")
                col_params.add_class("half-pane")
            else:
                col_roles.add_class("half-pane")
                col_inst.add_class("half-pane")
                col_params.add_class("hidden-pane")
        else:
            panes = [col_roles, col_inst, col_params]
            for idx, p in enumerate(panes):
                if idx == self._narrow_pane_idx % 3:
                    p.add_class("full-pane")
                else:
                    p.add_class("hidden-pane")

    def action_cycle_narrow_pane(self) -> None:
        self._narrow_pane_idx = (self._narrow_pane_idx + 1) % 3
        self._apply_responsive_layout(self.size.width)

    # ------------------------------------------------------------------
    # Navigation & Input-Safe Hotkeys (REQ-TUI-10)
    # ------------------------------------------------------------------
    def action_nav(self, view_id: str) -> None:
        switcher = self.query_one("#main-switcher", ContentSwitcher)
        switcher.current = view_id
        for vid in ("view-home", "view-workbench", "view-run-report", "view-system"):
            btn = self.query_one(f"#nav-{vid}", Button)
            if vid == view_id:
                btn.add_class("nav-active")
            else:
                btn.remove_class("nav-active")

    def action_jump_cleanup(self) -> None:
        self.action_nav("view-system")
        self.query_one("#sys-cleanup-table", DataTable).focus()

    def action_launch_wizard_run(self) -> None:
        view = self.query_one("#main-switcher", ContentSwitcher).current
        if view != "view-run-report":
            return
        try:
            binding = self._collect_wizard_binding()
        except Exception as exc:
            self.notify(f"绑定参数校验未通过: {exc}", severity="error")
            return
        self._launch_binding_worker(self.wizard_plan, binding)

    def action_quit_if_nav(self) -> None:
        if not self._is_text_editing():
            self.exit()

    def on_key(self, event: events.Key) -> None:
        if self._is_text_editing():
            return
        key = event.key
        if key in ("1", "2", "3", "4"):
            mapping = {
                "1": "view-home",
                "2": "view-workbench",
                "3": "view-run-report",
                "4": "view-system",
            }
            self.action_nav(mapping[key])
            event.stop()
            return
        if key == "exclamation_mark":
            self.action_jump_cleanup()
            event.stop()
            return

        active_view = self.query_one("#main-switcher", ContentSwitcher).current
        if active_view == "view-workbench":
            if self._wb_pending is not None and key in ("y", "n"):
                self._resolve_pending_edit(keep=key == "y")
                event.stop()
                return
            if key == "n" and self._wb_pending is None:
                self._open_new_host_type()
                event.stop()
                return
            if key == "a":
                self._open_template_picker()
                event.stop()
            elif key == "c":
                self._open_custom_shell_editor()
                event.stop()
            elif key == "d":
                self._open_clone_modal()
                event.stop()
            elif key == "s":
                self.action_save_draft()
                event.stop()
            elif key == "v":
                self._validate_current_plan()
                event.stop()
            elif key == "t":
                self._trial_run_current_draft()
                event.stop()
            elif key == "r":
                self._release_and_export_current_plan()
                event.stop()
            elif key == "x":
                self._delete_selected_instance()
                event.stop()
            elif key == "u":
                self._undo_delete_instance()
                event.stop()
            elif key == "space":
                self._toggle_selected_instance()
                event.stop()
        elif active_view == "view-run-report":
            rr_pane = self.query_one("#rr-switcher", ContentSwitcher).current
            if rr_pane == "rr-pane-report":
                if key == "f":
                    self._toggle_report_anomaly_filter()
                    event.stop()
                elif key == "g":
                    self._show_compiled_goss_modal()
                    event.stop()
                elif key == "r":
                    self._rerun_from_report(partial_only=False)
                    event.stop()
                elif key == "t":
                    self._rerun_from_report(partial_only=True)
                    event.stop()

    # ------------------------------------------------------------------
    # Table Initialization & Full Refresh
    # ------------------------------------------------------------------
    def _setup_tables(self) -> None:
        home_tbl = self.query_one("#home-runs-table", DataTable)
        home_tbl.add_columns("Run ID", "方案与版本", "范围", "就绪结论", "门禁资格", "完成时间")

        roles_tbl = self.query_one("#wb-roles-table", DataTable)
        roles_tbl.add_columns("ID", "名称", "最少节点")

        inst_tbl = self.query_one("#wb-inst-table", DataTable)
        inst_tbl.add_columns("启停", "模板", "重要性", "标题")

        live_tbl = self.query_one("#live-phase-table", DataTable)
        live_tbl.add_columns("主机名", "当前阶段 (1~9)", "实时说明")

        cat_tbl = self.query_one("#sys-cat-table", DataTable)
        cat_tbl.add_columns("领域", "组号", "中文名称", "成熟度", "模板ID")

        cln_tbl = self.query_one("#sys-cleanup-table", DataTable)
        cln_tbl.add_columns("清理ID", "主机", "状态", "存活PID", "受控远端目录")

    def _refresh_all_views(self) -> None:
        self._refresh_cleanup_badge()
        self._refresh_home_runs()
        self._refresh_workbench_plan_selector()
        self._refresh_workbench()
        self._refresh_wizard_plan_selector()
        self._rebuild_wizard_role_and_overrides()
        self._refresh_wizard_preview()
        self._refresh_report_history_selector()
        self._refresh_report_view()
        self._refresh_system_catalog("")
        self._refresh_system_cleanups()

    def _refresh_cleanup_badge(self) -> None:
        pending = self.store.pending_cleanup_records()
        btn = self.query_one("#cleanup-badge", Button)
        if pending:
            btn.label = f"⚠待清理: {len(pending)}"
            btn.add_class("cleanup-alert")
        else:
            btn.label = "清理: 0"
            btn.remove_class("cleanup-alert")

    def _refresh_home_runs(self) -> None:
        tbl = self.query_one("#home-runs-table", DataTable)
        tbl.clear()
        for rep in self.store.list_run_reports()[:20]:
            gate_str = "✔ 可放行" if rep.gate_eligible else "✖ 禁门禁"
            tbl.add_row(
                rep.run_id,
                f"{rep.plan_id}@{rep.plan_version} ({rep.plan_state.value})",
                rep.run_scope.value,
                rep.readiness.value,
                gate_str,
                rep.finished_at or rep.created_at,
                key=rep.run_id,
            )

    # ------------------------------------------------------------------
    # Act 2: Workbench Logic
    # ------------------------------------------------------------------
    def _plan_key(self, p: PlanDocument) -> str:
        return f"{p.state.value}:{p.plan_id}:{p.version}"

    def _refresh_workbench_plan_selector(self) -> None:
        plans = self.store.list_plans()
        options = [
            (f"[{p.state.value}] {p.name} ({p.plan_id}@{p.version})", self._plan_key(p))
            for p in plans
        ]
        sel = self.query_one("#wb-plan-select", Select)
        cur_key = self._plan_key(self.current_plan)
        if not any(val == cur_key for _, val in options):
            options.insert(
                0,
                (
                    f"[{self.current_plan.state.value}] {self.current_plan.name} ({self.current_plan.plan_id}@{self.current_plan.version})",
                    cur_key,
                ),
            )
        sel.set_options(options)
        sel.value = cur_key

    def _ensure_editable_draft_copy(self) -> None:
        """Editing an immutable RELEASED version automatically branches to a new DRAFT version (REQ-PLAN-06)."""
        if self.current_plan.state == PlanState.RELEASED:
            next_ver = _bump_patch_version(self.current_plan.version)
            self.current_plan = self.current_plan.model_copy(
                update={"state": PlanState.DRAFT, "version": next_ver, "release_manifest": None},
                deep=True,
            )
            self.notify(f"已发布版本不可原地修改，已自动派生新草稿版本 v{next_ver} (REQ-PLAN-06)")

    def _refresh_workbench(self) -> None:
        p = self.current_plan
        state_label = {
            PlanState.DRAFT: "草稿",
            PlanState.RELEASED: "已发布",
            PlanState.VALIDATED: "已校验",
            PlanState.RETIRED: "已退役",
            PlanState.TRIAL_SNAPSHOT: "试跑快照",
        }.get(p.state, p.state.value)
        self.query_one("#wb-plan-summary", Static).update(f"{state_label}  v{p.version}")
        self.query_one("#wb-compat-info", Static).update(
            f"OS {', '.join(p.compatibility.allowed_os)}    Arch {', '.join(p.compatibility.allowed_arch)}"
        )

        roles_tbl = self.query_one("#wb-roles-table", DataTable)
        roles_tbl.clear()
        for r in p.roles:
            roles_tbl.add_row(r.role_id, r.name, str(r.min_nodes), key=r.role_id)

        if p.roles and not any(r.role_id == self.selected_role_id for r in p.roles):
            self.selected_role_id = p.roles[0].role_id

        self._refresh_workbench_instances()
        self._refresh_workbench_params()

    def _refresh_workbench_instances(self) -> None:
        inst_tbl = self.query_one("#wb-inst-table", DataTable)
        inst_tbl.clear()
        role = self.current_plan.get_role(self.selected_role_id)
        if role is None:
            return
        templates = self.store.get_all_available_templates(self.current_plan)
        for inst in role.instances:
            tpl = templates.get(inst.template_id)
            priv_mark = "锁" if (tpl and tpl.privilege_group == PrivilegeGroup.PRIVILEGED) else ""
            inst_tbl.add_row(
                "开" if inst.enabled else "关",
                f"{inst.template_id}{priv_mark}",
                inst.importance.value,
                inst.title or (tpl.name_zh if tpl else ""),
                key=inst.instance_id,
            )
        if role.instances and not any(i.instance_id == self.selected_instance_id for i in role.instances):
            self.selected_instance_id = role.instances[0].instance_id

    def _get_selected_instance(self) -> tuple[RoleSpec | None, CheckInstance | None, TemplateSpec | None]:
        role = self.current_plan.get_role(self.selected_role_id)
        if role is None:
            return None, None, None
        inst = next((i for i in role.instances if i.instance_id == self.selected_instance_id), None)
        if inst is None:
            return role, None, None
        tpl = self.store.get_all_available_templates(self.current_plan).get(inst.template_id)
        return role, inst, tpl

    def _refresh_workbench_params(self) -> None:
        meta_lbl = self.query_one("#wb-param-meta", Static)
        fields_box = self.query_one("#wb-param-fields", Vertical)
        self._wb_loading = True
        fields_box.remove_children()
        self._wb_title_input = None
        self._wb_importance_select = None
        self._wb_param_widgets.clear()

        role, inst, tpl = self._get_selected_instance()
        if role is None or inst is None or tpl is None:
            meta_lbl.update("未选中检查实例")
            self.call_after_refresh(self._finish_param_load)
            return

        meta_lbl.update(
            f"[bold]{inst.instance_id}[/bold] → [{tpl.template_id}] {tpl.name_zh}\n"
            f"权限组: {tpl.privilege_group.value} | 证据深度: {tpl.evidence_depth.value}"
        )
        title_inp = Input(value=inst.title or tpl.name_zh)
        imp_sel: Select[Any] = Select(
            [("必需", "REQUIRED"), ("警告", "WARNING"), ("信息", "INFO")],
            value=inst.importance.value,
            allow_blank=False,
        )
        self._wb_title_input = title_inp
        self._wb_importance_select = imp_sel
        fields_box.mount(Label("实例标题"))
        fields_box.mount(title_inp)
        fields_box.mount(Label("重要性"))
        fields_box.mount(imp_sel)

        for p_spec in tpl.parameters:
            is_explicit = p_spec.name in inst.params
            cur_val = inst.params.get(p_spec.name, p_spec.default)
            src_badge = "实例显式赋值" if is_explicit else f"继承模板默认 {p_spec.default!r}"
            val_str = (
                ",".join(str(x) for x in cur_val)
                if isinstance(cur_val, list)
                else ("" if cur_val is None else str(cur_val))
            )
            box = Vertical(classes="param-field-box")
            fields_box.mount(box)
            unit = f"，单位 {p_spec.unit}" if p_spec.unit else ""
            box.mount(Label(f"{p_spec.label_zh}（{p_spec.name}，{p_spec.param_type.value}{unit}）"))
            box.mount(Label(src_badge, classes="param-source"))
            ov_chk = Checkbox(
                "允许现场覆盖",
                value=(p_spec.name in inst.overridable_params),
            )
            if p_spec.param_type == ParamType.LIST and p_spec.enum_values:
                choices = self._mount_choice_checks(box, p_spec, cur_val)
                self._wb_param_widgets[p_spec.name] = (choices, ov_chk)
            else:
                val_widget = self._make_param_value_widget(p_spec, cur_val, val_str)
                self._wb_param_widgets[p_spec.name] = (val_widget, ov_chk)
                box.mount(val_widget)
            box.mount(ov_chk)
        self.call_after_refresh(self._finish_param_load)

    def _finish_param_load(self) -> None:
        self._wb_loading = False
        self._wb_dirty = False

    def _apply_workbench_param_edits(self, *, refresh: bool = True) -> None:
        role, inst, tpl = self._get_selected_instance()
        if role is None or inst is None or tpl is None:
            return
        if self._wb_title_input is None or self._wb_importance_select is None:
            return
        new_title = self._wb_title_input.value.strip()
        imp_val = str(self._wb_importance_select.value)
        raw_values = {
            name: (self._read_param_widget(widget), chk.value)
            for name, (widget, chk) in self._wb_param_widgets.items()
        }

        self._ensure_editable_draft_copy()
        role, inst, tpl = self._get_selected_instance()
        assert inst is not None and tpl is not None

        inst.title = new_title
        inst.importance = Importance(imp_val)

        new_params: dict[str, Any] = {}
        new_ov: list[str] = []
        for p_spec in tpl.parameters:
            raw_value, ov_checked = raw_values.get(p_spec.name, ("", False))
            if ov_checked:
                new_ov.append(p_spec.name)
            parsed_val = (
                raw_value
                if isinstance(raw_value, list)
                else self._coerce_param_input(p_spec.param_type, raw_value)
            )
            if parsed_val != p_spec.default:
                new_params[p_spec.name] = parsed_val

        inst.params = new_params
        inst.overridable_params = new_ov
        self._wb_dirty = False
        if refresh:
            self._refresh_workbench()

    def _mount_choice_checks(
        self, parent: Vertical, p_spec: ParameterSpec, cur_val: Any
    ) -> list[tuple[str, Checkbox]]:
        selected = {str(item) for item in cur_val} if isinstance(cur_val, list) else set()
        if cur_val is not None and not isinstance(cur_val, list):
            selected.add(str(cur_val))
        candidates = list(p_spec.enum_values)
        for extra in sorted(selected):
            if extra not in candidates:
                candidates.append(extra)
        choice_box = Vertical(classes="param-choices")
        parent.mount(choice_box)
        checks: list[tuple[str, Checkbox]] = []
        for item in candidates:
            chk = Checkbox(item, value=item in selected)
            choice_box.mount(chk)
            checks.append((item, chk))
        return checks

    def _make_param_value_widget(
        self, p_spec: ParameterSpec, cur_val: Any, val_str: str
    ) -> Input | Select[Any]:
        if p_spec.param_type == ParamType.ENUM and p_spec.enum_values:
            current = "" if cur_val is None else str(cur_val)
            options = [(item, item) for item in p_spec.enum_values]
            if current and current not in p_spec.enum_values:
                options.insert(0, (current, current))
            selected = current if current in {item for item, _ in options} else p_spec.enum_values[0]
            return Select(options, value=selected, allow_blank=False)
        return Input(value=val_str)

    def _read_param_widget(
        self, widget: Input | Select[Any] | list[tuple[str, Checkbox]]
    ) -> str | list[str]:
        if isinstance(widget, list):
            return [item for item, chk in widget if chk.value]
        if isinstance(widget, Select):
            if widget.value is Select.BLANK:
                return ""
            return str(widget.value)
        return widget.value.strip()

    def _coerce_param_input(self, p_type: ParamType, raw: str) -> Any:
        if p_type in (ParamType.INTEGER, ParamType.PORT):
            try:
                return int(raw)
            except ValueError:
                return raw
        if p_type == ParamType.NUMBER:
            try:
                return float(raw)
            except ValueError:
                return raw
        if p_type == ParamType.BOOLEAN:
            return raw.lower() in ("true", "1", "yes", "on")
        if p_type == ParamType.LIST:
            return [x.strip() for x in raw.split(",") if x.strip()]
        return raw

    def _open_template_picker(self) -> None:
        role = self.current_plan.get_role(self.selected_role_id)
        if role is None:
            return
        avail = list(self.store.get_all_available_templates(self.current_plan).values())

        def on_picked(chosen: list[TemplateSpec] | None) -> None:
            if not chosen:
                return
            self._ensure_editable_draft_copy()
            r = self.current_plan.get_role(self.selected_role_id)
            if r is None:
                return
            for tpl in chosen:
                new_id = f"{r.role_id}_{tpl.template_id.lower().replace('.', '_')}_{uuid.uuid4().hex[:4]}"
                default_ov = [p.name for p in tpl.parameters if p.overridable_by_default]
                r.instances.append(
                    CheckInstance(
                        instance_id=new_id,
                        title=tpl.name_zh,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        template_digest=tpl.digest,
                        overridable_params=default_ov,
                    )
                )
                self.selected_instance_id = new_id
            self._refresh_workbench()
            self.notify(f"已向角色『{r.name}』添加 {len(chosen)} 条独立检查实例")

        self.push_screen(TemplatePickerModal(avail, role.name), on_picked)

    def _open_custom_shell_editor(self) -> None:
        _, _inst, tpl = self._get_selected_instance()
        existing_custom = tpl if (tpl and tpl.kind == TemplateKind.CUSTOM_SHELL) else None

        def on_saved(res: tuple[TemplateSpec, bool] | None) -> None:
            if res is None:
                return
            new_tpl, promote = res
            self._ensure_editable_draft_copy()
            # Upsert plan-local embedded template
            self.current_plan.embedded_templates = [
                t for t in self.current_plan.embedded_templates if t.template_id != new_tpl.template_id
            ] + [new_tpl]
            if promote:
                try:
                    pub = self.store.promote_custom_template(self.current_plan, new_tpl.template_id)
                    self.notify(f"已提升为用户公共模板: {pub.template_id}@{pub.version}（原方案保持不变，REQ-TPL-16）")
                except PlanLifecycleError as exc:
                    self.notify(exc.message, severity="error")

            r = self.current_plan.get_role(self.selected_role_id)
            if r is not None and existing_custom is None:
                new_id = f"{r.role_id}_shell_{uuid.uuid4().hex[:5]}"
                r.instances.append(
                    CheckInstance(
                        instance_id=new_id,
                        title=new_tpl.name_zh,
                        template_id=new_tpl.template_id,
                        template_version=new_tpl.version,
                        template_digest=new_tpl.digest,
                        overridable_params=[p.name for p in new_tpl.parameters],
                    )
                )
                self.selected_instance_id = new_id
            self._refresh_workbench()

        self.push_screen(CustomShellModal(existing_custom), on_saved)

    def _open_clone_modal(self) -> None:
        role, inst, _ = self._get_selected_instance()
        if role is None or inst is None:
            return

        def on_target(target_role_id: str | None) -> None:
            if not target_role_id:
                return
            self._ensure_editable_draft_copy()
            cloned = self.current_plan.clone_instance_to_role(
                role.role_id, inst.instance_id, target_role_id
            )
            self.selected_role_id = target_role_id
            self.selected_instance_id = cloned.instance_id
            self._refresh_workbench()
            self.notify(f"已复制为新独立实例 {cloned.instance_id} (目标角色: {target_role_id})")

        self.push_screen(
            CloneToRoleModal(self.current_plan.roles, inst.title or inst.instance_id),
            on_target,
        )

    def _toggle_selected_instance(self) -> None:
        _, inst, _ = self._get_selected_instance()
        if inst is None:
            return
        self._ensure_editable_draft_copy()
        _, inst2, _ = self._get_selected_instance()
        if inst2 is not None:
            inst2.enabled = not inst2.enabled
            self._refresh_workbench_instances()

    def _delete_selected_instance(self) -> None:
        role, inst, _ = self._get_selected_instance()
        if role is None or inst is None:
            return
        self._ensure_editable_draft_copy()
        r2 = self.current_plan.get_role(self.selected_role_id)
        if r2 is None:
            return
        for idx, item in enumerate(r2.instances):
            if item.instance_id == inst.instance_id:
                removed = r2.instances.pop(idx)
                self._undo_deleted_stack.append((r2.role_id, idx, removed))
                self.selected_instance_id = r2.instances[0].instance_id if r2.instances else ""
                self._refresh_workbench()
                self._set_workbench_status(
                    f"已删除实例 {removed.instance_id}。按 u 可撤销。"
                )
                break

    def _set_workbench_status(self, message: str) -> None:
        self.query_one("#wb-diag-bar", Static).update(message)

    def _mark_selected(self, selected: str, *ids: str) -> None:
        for wid in ids:
            btn = self.query_one(f"#{wid}", Button)
            if wid == selected:
                btn.add_class("is-selected")
            else:
                btn.remove_class("is-selected")

    def _widget_is_workbench_field(self, widget: object) -> bool:
        if widget is self._wb_title_input or widget is self._wb_importance_select:
            return True
        for value_widget, ov_chk in self._wb_param_widgets.values():
            if widget is ov_chk or widget is value_widget:
                return True
            if isinstance(value_widget, list) and any(widget is chk for _, chk in value_widget):
                return True
        return False

    def _ask_or_move(self, proceed: Callable[[], None]) -> None:
        if not self._wb_dirty:
            proceed()
            return
        self._wb_pending = proceed
        self._set_workbench_status("有未保存修改。按 y 保留并继续，按 n 放弃。")

    def _resolve_pending_edit(self, *, keep: bool) -> None:
        pending = self._wb_pending
        self._wb_pending = None
        if keep:
            self._apply_workbench_param_edits(refresh=False)
        self._wb_dirty = False
        self._set_workbench_status("")
        if pending is not None:
            pending()

    def _open_new_host_type(self) -> None:
        def on_id(rid: str | None) -> None:
            if not rid:
                return
            if self.current_plan.get_role(rid):
                self._set_workbench_status(f"主机类型 {rid} 已存在")
                return
            self._ensure_editable_draft_copy()
            self.current_plan.roles.append(RoleSpec(role_id=rid, name=rid, min_nodes=1))
            self.selected_role_id = rid
            self.selected_instance_id = ""
            self._refresh_workbench()
            self._set_workbench_status(f"已添加主机类型 {rid}")

        self.push_screen(HostTypeModal(), on_id)

    def _undo_delete_instance(self) -> None:
        if not self._undo_deleted_stack:
            self._set_workbench_status("没有可撤销的删除")
            return
        role_id, idx, inst = self._undo_deleted_stack.pop()
        r = self.current_plan.get_role(role_id)
        if r is not None:
            r.instances.insert(min(idx, len(r.instances)), inst)
            self.selected_role_id = role_id
            self.selected_instance_id = inst.instance_id
            self._wb_dirty = False
            self._refresh_workbench()
            self._set_workbench_status(f"已恢复实例 {inst.instance_id}")

    def action_save_draft(self) -> None:
        view = self.query_one("#main-switcher", ContentSwitcher).current
        if view != "view-workbench":
            return
        self._apply_workbench_param_edits(refresh=False)
        self._ensure_editable_draft_copy()
        self.current_plan = self.store.save_draft(self.current_plan)
        self._wb_dirty = False
        self._refresh_workbench_plan_selector()
        self._refresh_workbench()
        self._set_workbench_status(
            f"已保存草稿 {self.current_plan.plan_id}@{self.current_plan.version}"
        )

    def _validate_current_plan(self) -> bool:
        diags = self.store.validate_plan(self.current_plan)
        bar = self.query_one("#wb-diag-bar", Static)
        errors = [d for d in diags if d.severity == "ERROR"]
        warns = [d for d in diags if d.severity != "ERROR"]
        if errors:
            first = errors[0]
            if first.role_id:
                self.selected_role_id = first.role_id
            if first.instance_id:
                self.selected_instance_id = first.instance_id
            self._refresh_workbench()
            bar.update(f"[red]✖ 静态校验发现 {len(errors)} 项错误（首项 [{first.code}]: {first.message}）[/red]")
            return False
        msg = f"[green]✔ 静态校验全部通过（角色 {len(self.current_plan.roles)} 个）[/green]"
        if warns:
            msg += f" [yellow]| {len(warns)} 条提示: {warns[0].message}[/yellow]"
        bar.update(msg)
        return True

    def _trial_run_current_draft(self) -> None:
        try:
            snapshot = self.store.create_trial_snapshot(self.current_plan)
        except PlanLifecycleError as exc:
            self.query_one("#wb-diag-bar", Static).update(f"[red]试跑拦截: {exc.message}[/red]")
            self.notify(exc.message, severity="error")
            return
        self.wizard_plan = snapshot
        self._refresh_wizard_plan_selector()
        self._rebuild_wizard_role_and_overrides()
        self._refresh_wizard_preview()
        self.action_nav("view-run-report")
        self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-wizard"
        self.notify("已封装只读 TRIAL_SNAPSHOT 试跑快照（不占用正式版本号，且严禁用于门禁放行）")

    def _release_and_export_current_plan(self) -> None:
        target_ver = self.current_plan.version
        if (self.store.released_dir / f"{self.current_plan.plan_id}__{target_ver}.yaml").exists():
            target_ver = _bump_patch_version(target_ver)
        try:
            released = self.store.release_plan(self.current_plan, version=target_ver)
        except PlanLifecycleError as exc:
            self.query_one("#wb-diag-bar", Static).update(f"[red]发布失败 [{exc.code}]: {exc.message}[/red]")
            self.notify(exc.message, severity="error")
            return
        export_path = self.store.exports_dir / f"{released.plan_id}-{released.version}.yaml"
        yaml_text = self.store.export_plan_yaml(released, export_path)
        self.current_plan = released.model_copy(deep=True)
        self.wizard_plan = released
        self._refresh_all_views()
        self.push_screen(
            YamlViewerModal(
                f"正式发布成功 — {released.plan_id}@{released.version} (RELEASED)",
                yaml_text,
                saved_hint=f"自包含单文件 YAML 已导出至: {export_path} | Digest: {released.digest}",
            )
        )

    # ------------------------------------------------------------------
    # Act 3: 4-Step Run Wizard & Live 9-Phase Execution
    # ------------------------------------------------------------------
    def _refresh_wizard_plan_selector(self) -> None:
        plans = [p for p in self.store.list_plans() if p.state == PlanState.RELEASED]
        options = [
            (f"[RELEASED] {p.name} ({p.plan_id}@{p.version})", self._plan_key(p))
            for p in plans
        ]
        cur_k = self._plan_key(self.wizard_plan)
        if not any(v == cur_k for _, v in options):
            options.insert(
                0,
                (
                    f"[{self.wizard_plan.state.value}] {self.wizard_plan.name} ({self.wizard_plan.plan_id}@{self.wizard_plan.version})",
                    cur_k,
                ),
            )
        sel = self.query_one("#wiz-plan-select", Select)
        sel.set_options(options)
        sel.value = cur_k
        self.query_one("#wiz-plan-info", Static).update(
            f"当前绑定方案: [bold]{self.wizard_plan.plan_id}@{self.wizard_plan.version}[/bold] "
            f"[{self.wizard_plan.state.value}] | 摘要: {str(self.wizard_plan.digest)[:26]}..."
        )

    def _parse_wizard_inventory(self) -> InventorySnapshot:
        raw_text = self.query_one("#inv-textarea", TextArea).text
        ssh_user = self.query_one("#wiz-ssh-user", Input).value.strip() or "ops"
        mode = "quick_ip" if self.wizard_inv_mode == "quick_ip" else "auto"
        return parse_inventory_auto(raw_text, mode=mode, default_user=ssh_user)

    def _rebuild_wizard_role_and_overrides(self) -> None:
        role_box = self.query_one("#wiz-role-map-box", Vertical)
        ov_box = self.query_one("#wiz-overrides-box", Vertical)
        role_box.remove_children()
        ov_box.remove_children()
        self._wiz_role_inputs.clear()
        self._wiz_ov_inputs.clear()

        try:
            inv = self._parse_wizard_inventory()
            suggested = auto_suggest_role_mapping(self.wizard_plan, inv)
        except InventoryParseError as exc:
            role_box.mount(Static(f"[red]清单解析错误 [{exc.code}]: {exc.message}[/red]"))
            return

        role_box.mount(Label("业务角色 → 目标主机名列表（逗号分隔，单角色自动绑定 / 同名组自动匹配）："))
        for role in self.wizard_plan.roles:
            hosts_csv = ", ".join(suggested.get(role.role_id, []))
            role_box.mount(
                Label(f"  角色『{role.name} ({role.role_id})』[最少 {role.min_nodes} 台]:")
            )
            r_inp = Input(value=hosts_csv)
            self._wiz_role_inputs[role.role_id] = r_inp
            role_box.mount(r_inp)

        # Render ONLY author-approved overridable parameters (CHG-002)
        templates = self.store.get_all_available_templates(self.wizard_plan)
        has_any_ov = False
        for role in self.wizard_plan.roles:
            for inst in role.instances:
                if not inst.enabled or not inst.overridable_params:
                    continue
                tpl = templates.get(inst.template_id)
                if tpl is None:
                    continue
                p_map = {p.name: p for p in tpl.parameters}
                for p_name in inst.overridable_params:
                    p_spec = p_map.get(p_name)
                    if p_spec is None:
                        continue
                    if not has_any_ov:
                        ov_box.mount(Label("现场允许调整的参数覆盖（留空保持方案设定值，绝不反写原方案）："))
                        has_any_ov = True
                    base_val = inst.params.get(p_name, p_spec.default)
                    ov_box.mount(
                        Label(f"  [{role.role_id}/{inst.instance_id}] {p_spec.label_zh} ({p_name}, 方案值={base_val!r}):")
                    )
                    ov_inp = Input(placeholder=f"保持方案默认 ({base_val})")
                    self._wiz_ov_inputs[(role.role_id, inst.instance_id, p_name)] = ov_inp
                    ov_box.mount(ov_inp)

    def _collect_wizard_binding(self) -> RunBinding:
        inv = self._parse_wizard_inventory()
        role_to_hosts: dict[str, list[str]] = {}
        for role in self.wizard_plan.roles:
            r_inp = self._wiz_role_inputs.get(role.role_id)
            if r_inp is not None:
                raw_csv = r_inp.value
                role_to_hosts[role.role_id] = [h.strip() for h in raw_csv.split(",") if h.strip()]
            else:
                role_to_hosts[role.role_id] = auto_suggest_role_mapping(self.wizard_plan, inv).get(
                    role.role_id, []
                )

        overrides: list[ParameterOverrideRule] = []
        templates = self.store.get_all_available_templates(self.wizard_plan)
        for role in self.wizard_plan.roles:
            for inst in role.instances:
                tpl = templates.get(inst.template_id)
                if tpl is None:
                    continue
                p_map = {p.name: p for p in tpl.parameters}
                for p_name in inst.overridable_params:
                    ov_inp = self._wiz_ov_inputs.get((role.role_id, inst.instance_id, p_name))
                    if ov_inp is None:
                        continue
                    txt = ov_inp.value.strip()
                    if txt == "":
                        continue
                    coerced = self._coerce_param_input(p_map[p_name].param_type, txt)
                    overrides.append(
                        ParameterOverrideRule(
                            scope_type="group",
                            scope_target="all",
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                            param_name=p_name,
                            value=coerced,
                        )
                    )

        approve_priv = self.query_one("#wiz-approve-priv", Checkbox).value
        approve_root = self.query_one("#wiz-approve-root", Checkbox).value
        sim_profile = str(self.query_one("#wiz-sim-profile", Select).value)

        priv_ids: list[str] = []
        if approve_priv:
            for r in self.wizard_plan.roles:
                for inst in r.instances:
                    tpl = templates.get(inst.template_id)
                    if tpl and tpl.privilege_group == PrivilegeGroup.PRIVILEGED:
                        priv_ids.append(tpl.template_id)

        secret_txt = self.query_one("#wiz-secret", Input).value
        cred_token: str | None = None
        if secret_txt:
            cred_token = self.controller.vault.put_secret(secret_txt)

        return RunBinding(
            plan_id=self.wizard_plan.plan_id,
            plan_version=self.wizard_plan.version,
            plan_state=self.wizard_plan.state,
            plan_digest=self.wizard_plan.digest or self.wizard_plan.compute_semantic_digest(),
            inventory=inv,
            role_to_hosts=role_to_hosts,
            runtime_overrides=overrides,
            approved_privileged_templates=sorted(set(priv_ids)),
            approve_root_ssh=approve_root,
            simulation_profile=sim_profile,
            credential_token=cred_token,
        )

    def _refresh_wizard_preview(self) -> None:
        lbl = self.query_one("#wiz-preview-text", Static)
        try:
            binding = self._collect_wizard_binding()
            # Burn preview-only secret immediately
            self.controller.vault.burn_secret(binding.credential_token)
            preview = self.controller.preview_run(self.wizard_plan, binding)
        except Exception as exc:
            lbl.update(f"[red]预览阻断: {exc}[/red]")
            return

        diags_txt = "\n".join(
            f"  • [{d.severity}] {d.message}" for d in preview.diagnostics
        ) or "  • 无静态阻断或风险告警"
        lbl.update(
            f"[bold]静态执行预览（零 SSH 离线演算）[/bold]\n"
            f"目标主机总数: {preview.host_count} 台 | 候选检查总次数: {preview.total_candidate_checks} 项\n"
            f"涉及内置特权模板: {', '.join(preview.privileged_templates_needed) or '无'} | "
            f"root SSH 主机: {', '.join(preview.root_ssh_hosts) or '无'}\n"
            f"方案内自定义 Shell: {', '.join(preview.custom_shell_instances) or '无'} | "
            f"主动网络探测: {', '.join(preview.active_network_probes) or '无'}\n"
            f"诊断提示:\n{diags_txt}"
        )

    @work(exclusive=True)
    async def _launch_binding_worker(self, plan: PlanDocument, binding: RunBinding) -> None:
        self.last_binding = binding.model_copy(
            update={"credential_token": None, "idempotency_key": f"req-{uuid.uuid4().hex[:12]}"},
            deep=True,
        )
        # Clear password widget immediately so plaintext doesn't linger in UI
        self.query_one("#wiz-secret", Input).value = ""

        self.action_nav("view-run-report")
        self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-live"
        self._live_host_phases.clear()
        live_tbl = self.query_one("#live-phase-table", DataTable)
        live_tbl.clear()
        self.query_one("#live-run-banner", Static).update(
            f"[cyan]正在执行 {plan.plan_id}@{plan.version} ({binding.run_scope.value} | 仿真画像={binding.simulation_profile})...[/cyan]"
        )

        phase_order = list(HostPhase)

        def on_phase(host_name: str, phase: HostPhase, detail: str) -> None:
            self._live_host_phases[host_name] = (phase, detail)
            live_tbl.clear()
            for h_key, (ph, msg) in self._live_host_phases.items():
                p_num = phase_order.index(ph) + 1
                live_tbl.add_row(h_key, f"[{p_num}/9] {ph.value}", msg)

        try:
            report = await self.controller.execute_run(plan, binding, on_phase=on_phase)
        except (ControllerSecurityError, Exception) as exc:
            self.query_one("#live-run-banner", Static).update(f"[red]执行拒绝/中断: {exc}[/red]")
            self.notify(str(exc), severity="error")
            return

        self.current_report = report
        self._refresh_cleanup_badge()
        self._refresh_home_runs()
        self._refresh_report_history_selector()
        self._refresh_report_view()
        self._refresh_system_cleanups()
        self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-report"
        self.notify(f"检查已完成: {report.run_id} → Readiness={report.readiness.value}")

    # ------------------------------------------------------------------
    # Act 4A: Dual-Perspective Report & Dual Re-Run Modes
    # ------------------------------------------------------------------
    def _refresh_report_history_selector(self) -> None:
        reports = self.store.list_run_reports()
        sel = self.query_one("#rr-history-select", Select)
        opts = [
            (f"{r.run_id} | {r.plan_id}@{r.plan_version} | {r.readiness.value}", r.run_id)
            for r in reports
        ]
        sel.set_options(opts)
        if self.current_report and any(v == self.current_report.run_id for _, v in opts):
            sel.value = self.current_report.run_id

    def _refresh_report_view(self) -> None:
        banner = self.query_one("#report-banner", Static)
        tbl = self.query_one("#report-table", DataTable)
        ev_pane = self.query_one("#report-evidence-detail", Static)
        tbl.clear(columns=True)
        self._report_row_evidence_map.clear()

        rep = self.current_report
        if rep is None:
            banner.update("当前尚无运行报告")
            return

        banner.remove_class("banner-ready", "banner-not-ready", "banner-unknown")
        if rep.readiness == ReadinessStatus.READY:
            banner.add_class("banner-ready")
        elif rep.readiness == ReadinessStatus.NOT_READY:
            banner.add_class("banner-not-ready")
        else:
            banner.add_class("banner-unknown")

        gate_badge = (
            "[bold green]✔ 满足自动部署门禁放行条件 (Exit 0)[/bold green]"
            if rep.gate_eligible
            else "[bold yellow]✖ 禁止用于正式门禁放行 (非 0 退出码)[/bold yellow]"
        )
        root_note = (
            f" | [bold red]⚠ 高风险 root SSH 审计主机: {','.join(rep.audit.high_risk_root_hosts)}[/bold red]"
            if rep.audit.high_risk_root_hosts
            else ""
        )
        banner.update(
            f"[bold]Run {rep.run_id}[/bold] | 方案: {rep.plan_id}@{rep.plan_version} [{rep.plan_state.value}] | "
            f"范围: [{rep.run_scope.value}] | 执行后端: [yellow]{rep.execution_backend} (非实机 Verified)[/yellow]{root_note}\n"
            f"最终就绪结论: [bold]{rep.readiness.value}[/bold]  —  {gate_badge}\n"
            f"结论依据: {rep.readiness_reason}"
        )

        anom_btn = self.query_one("#rep-toggle-anomaly", Button)
        anom_btn.label = f"[f]仅看异常: {'开' if self.report_only_anomalies else '关'}"

        if self.report_perspective == "host":
            tbl.add_columns("目标主机 / 控制面", "角色", "检查项", "重要性", "状态", "结论摘要")
            # Control-plane checks first
            for ctrl in rep.control_checks:
                if self.report_only_anomalies and ctrl.status == CheckStatus.PASS:
                    continue
                rk = f"ctrl:{ctrl.instance_id}"
                tbl.add_row(
                    "[_control 拓扑/聚合]",
                    "-",
                    ctrl.template_id,
                    ctrl.importance.value,
                    ctrl.status.value,
                    ctrl.summary,
                    key=rk,
                )
                self._report_row_evidence_map[rk] = self._format_check_evidence(
                    "_control", ctrl
                )
            for h in rep.hosts:
                if h.host_error:
                    rk = f"hosterr:{h.host_name}"
                    tbl.add_row(
                        h.host_name,
                        ",".join(h.roles),
                        "HOST_LIFECYCLE",
                        "REQUIRED",
                        "ERROR",
                        h.host_error,
                        key=rk,
                    )
                    self._report_row_evidence_map[rk] = f"[red]主机级执行中断:[/red]\n{h.host_error}"
                for c in h.check_results:
                    if self.report_only_anomalies and c.status in (CheckStatus.PASS, CheckStatus.SKIP):
                        continue
                    rk = f"hc:{h.host_name}:{c.instance_id}"
                    tbl.add_row(
                        h.host_name,
                        c.role_id,
                        f"{c.template_id} ({c.instance_id})",
                        c.importance.value,
                        c.status.value,
                        c.summary,
                        key=rk,
                    )
                    self._report_row_evidence_map[rk] = self._format_check_evidence(
                        h.host_name, c
                    )
        else:
            # By-Check perspective (`[按检查项视角]`)
            tbl.add_columns("检查实例 (模板)", "角色", "重要性", "通过/总数", "聚合状态", "异常主机列表")
            grouped: dict[str, list[tuple[str, CheckInstanceResult]]] = {}
            for h in rep.hosts:
                for c in h.check_results:
                    grouped.setdefault(c.instance_id, []).append((h.host_name, c))

            for inst_id, pairs in grouped.items():
                sample = pairs[0][1]
                pass_cnt = sum(1 for _, c in pairs if c.status == CheckStatus.PASS)
                bad_hosts = [f"{hn}({c.status.value})" for hn, c in pairs if c.status != CheckStatus.PASS]
                agg_st = (
                    "FAIL"
                    if any(c.status == CheckStatus.FAIL for _, c in pairs)
                    else ("ERROR" if bad_hosts else "PASS")
                )
                if self.report_only_anomalies and agg_st == "PASS":
                    continue
                rk = f"agg:{inst_id}"
                tbl.add_row(
                    f"{inst_id} [{sample.template_id}]",
                    sample.role_id,
                    sample.importance.value,
                    f"{pass_cnt}/{len(pairs)}",
                    agg_st,
                    ", ".join(bad_hosts) or "全部正常",
                    key=rk,
                )
                self._report_row_evidence_map[rk] = "\n\n".join(
                    self._format_check_evidence(hn, c) for hn, c in pairs
                )

        if self._report_row_evidence_map:
            first_key = next(iter(self._report_row_evidence_map))
            ev_pane.update(self._report_row_evidence_map[first_key])
        else:
            ev_pane.update("当前过滤条件下无记录")

    def _format_check_evidence(self, host_name: str, c: CheckInstanceResult) -> str:
        lines = [
            f"[bold]{host_name} :: {c.title} ({c.instance_id})[/bold]",
            f"模板: {c.template_id}@{c.template_version} | 重要性: {c.importance.value} | 状态: [bold]{c.status.value}[/bold]",
            f"结论: {c.summary}",
        ]
        if c.remediation_hint:
            lines.append(f"[yellow]修复建议: {c.remediation_hint}[/yellow]")
        if c.resolved_params:
            p_lines = [
                f"  • {k} = {'***' if v.sensitive else repr(v.value)}  [{v.source}]"
                for k, v in c.resolved_params.items()
            ]
            lines.append("生效参数溯源:\n" + "\n".join(p_lines))
        for ev in c.evidences:
            lines.append(
                f"── 证据 ({ev.goss_subrule_id}) ──\n"
                f"  Expected : {ev.expected}\n"
                f"  Observed : {ev.observed}\n"
                f"  来源/深度: {ev.source} / {ev.evidence_depth.value}\n"
                f"  执行身份 : {ev.execution_identity} ({ev.privilege_group.value})"
                + (" [⚠ ROOT SSH 强审计]" if ev.high_risk_root_audit else "")
                + (f"\n  窗口覆盖 : {ev.window_coverage}" if ev.window_coverage else "")
            )
        return "\n".join(lines)

    def _toggle_report_anomaly_filter(self) -> None:
        self.report_only_anomalies = not self.report_only_anomalies
        self._refresh_report_view()

    def _show_compiled_goss_modal(self) -> None:
        rep = self.current_report
        if rep is None or not rep.hosts:
            self.notify("当前报告无已编译的主机 Goss 工件")
            return
        chunks: list[str] = []
        for h in rep.hosts:
            chunks.append(f"# ==================== Host: {h.host_name} ({h.address}) ====================")
            if h.standard_artifact_yaml:
                chunks.append(f"# [STANDARD] digest={h.standard_artifact_digest}\n{h.standard_artifact_yaml}")
            if h.privileged_artifact_yaml:
                chunks.append(f"# [PRIVILEGED] digest={h.privileged_artifact_digest}\n{h.privileged_artifact_yaml}")
            if not h.standard_artifact_yaml and not h.privileged_artifact_yaml:
                chunks.append("# (演示历史记录未内联原始 YAML；发起一次新检查即可查看完整智能合并 Goss YAML)")
        self.push_screen(
            YamlViewerModal(
                f"真实编译生成的 Goss v0.4.9 工件 — {rep.run_id}",
                "\n".join(chunks),
            )
        )

    def _rerun_from_report(self, *, partial_only: bool) -> None:
        rep = self.current_report
        if rep is None:
            return
        plan = self.store.get_plan(rep.plan_id, rep.plan_version) or self.wizard_plan
        if self.last_binding and self.last_binding.plan_id == plan.plan_id:
            base_binding = self.last_binding.model_copy(
                update={"idempotency_key": f"req-{uuid.uuid4().hex[:12]}"}, deep=True
            )
        else:
            try:
                base_binding = self._collect_wizard_binding()
            except Exception as exc:
                self.notify(f"请先在执行向导确认主机清单: {exc}", severity="warning")
                return

        if partial_only:
            fault_hosts = [
                h.host_name
                for h in rep.hosts
                if h.host_error
                or any(
                    c.status in (CheckStatus.FAIL, CheckStatus.ERROR)
                    for c in h.check_results
                )
            ]
            if not fault_hosts:
                self.notify("当前报告没有异常主机，无需局部快测；可使用 [r] 全量正式复测")
                return
            base_binding = base_binding.model_copy(
                update={
                    "run_scope": RunScope.PARTIAL_SCOPE,
                    "selected_hosts_filter": fault_hosts,
                }
            )
        else:
            base_binding = base_binding.model_copy(
                update={"run_scope": RunScope.FULL, "selected_hosts_filter": []}
            )

        self._launch_binding_worker(plan, base_binding)

    # ------------------------------------------------------------------
    # Act 4B: 54-Group Catalog & Crash Cleanup Wizard
    # ------------------------------------------------------------------
    def _refresh_system_catalog(self, query: str) -> None:
        tbl = self.query_one("#sys-cat-table", DataTable)
        tbl.clear()
        q = query.strip().lower()
        for grp in get_catalog_groups():
            hay = f"{grp.domain_code} {grp.domain_name_zh} {grp.group_code} {grp.group_name_zh} {grp.group_name_en} {grp.maturity.value}".lower()
            if q and q not in hay:
                continue
            tbl.add_row(
                f"{grp.domain_code} {grp.domain_name_zh}",
                grp.group_code,
                grp.group_name_zh,
                grp.maturity.value,
                grp.template_id or "(规划中禁引用)",
                key=grp.group_code,
            )

    def _refresh_system_cleanups(self) -> None:
        tbl = self.query_one("#sys-cleanup-table", DataTable)
        tbl.clear()
        records = self.store.list_cleanup_records()
        for rec in records:
            pid_str = f"PID={rec.process_pid} (存活)" if rec.live_process_detected else "无"
            tbl.add_row(
                rec.cleanup_id,
                rec.host_name,
                rec.status.value,
                pid_str,
                rec.remote_dir,
                key=rec.cleanup_id,
            )
        detail = self.query_one("#sys-cleanup-detail", Static)
        if records:
            r0 = records[0]
            detail.update(
                f"选中项详情: {r0.cleanup_id} | Run={r0.run_id} | 状态={r0.status.value}"
                + (f" | WAIVED原因: {r0.waived_reason}" if r0.waived_reason else "")
            )
        else:
            detail.update("当前无残留清理/恢复记录")

    def _open_selected_cleanup_modal(self) -> None:
        tbl = self.query_one("#sys-cleanup-table", DataTable)
        records = {r.cleanup_id: r for r in self.store.list_cleanup_records()}
        if not records or tbl.row_count == 0:
            return
        row_idx = tbl.cursor_row
        cleanup_id = str(tbl.get_row_at(row_idx)[0])
        rec = records.get(cleanup_id)
        if rec is None:
            return

        def on_action(decision: dict[str, Any] | None) -> None:
            if decision is None:
                return
            self._resolve_cleanup_worker(rec.cleanup_id, decision)

        self.push_screen(CleanupActionModal(rec), on_action)

    @work
    async def _resolve_cleanup_worker(self, cleanup_id: str, decision: dict[str, Any]) -> None:
        try:
            if decision["mode"] == "waive":
                await self.controller.resolve_cleanup_item(
                    cleanup_id, waive_reason=str(decision["reason"])
                )
                self.notify(f"已将 {cleanup_id} 标记为 WAIVED 并写入永久审计摘要")
            else:
                await self.controller.resolve_cleanup_item(
                    cleanup_id, confirm_kill_live_process=bool(decision.get("confirm_kill", False))
                )
                self.notify(f"已完成三重归属指纹核验并安全清理 {cleanup_id}")
        except Exception as exc:
            self.notify(str(exc), severity="error")
        self._refresh_cleanup_badge()
        self._refresh_system_cleanups()

    # ------------------------------------------------------------------
    # Unified Widget Event Handlers
    # ------------------------------------------------------------------
    def on_button_pressed(self, event: Button.Pressed) -> None:
        bid = event.button.id or ""

        # Top nav
        if bid.startswith("nav-"):
            self.action_nav(bid.removeprefix("nav-"))
            return
        if bid == "cleanup-badge":
            self.action_jump_cleanup()
            return

        # Home screen
        if bid == "home-start-run":
            self.action_nav("view-run-report")
            self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-wizard"
        elif bid == "home-import-plan" or bid == "wiz-paste-plan":
            def on_imp(imported: PlanDocument | None) -> None:
                if imported is None:
                    return
                if imported.state == PlanState.RELEASED:
                    self.wizard_plan = imported
                else:
                    self.current_plan = imported
                self._refresh_all_views()
                self.notify(f"方案导入成功: {imported.plan_id}@{imported.version} [{imported.state.value}]")

            self.push_screen(PasteImportModal(self.store), on_imp)
        elif bid == "home-rerun-last":
            self.action_nav("view-run-report")
        elif bid == "home-new-plan":
            def on_created(new_p: PlanDocument | None) -> None:
                if new_p is None:
                    return
                saved = self.store.save_draft(new_p)
                self.current_plan = saved
                self.selected_role_id = saved.roles[0].role_id
                self.selected_instance_id = ""
                self._refresh_all_views()
                self.action_nav("view-workbench")

            self.push_screen(NewPlanModal(), on_created)
        elif bid == "home-open-wb":
            self.action_nav("view-workbench")
        elif bid == "home-browse-tpl":
            self.action_nav("view-system")

        elif bid == "rr-tab-wizard":
            self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-wizard"
            self._mark_selected("rr-tab-wizard", "rr-tab-wizard", "rr-tab-live", "rr-tab-report")
        elif bid == "rr-tab-live":
            self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-live"
            self._mark_selected("rr-tab-live", "rr-tab-wizard", "rr-tab-live", "rr-tab-report")
        elif bid == "rr-tab-report" or bid == "live-goto-report":
            self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-report"
            self._mark_selected("rr-tab-report", "rr-tab-wizard", "rr-tab-live", "rr-tab-report")
        elif bid == "wiz-mode-ini":
            self.wizard_inv_mode = "ansible_ini"
            self.query_one("#inv-textarea", TextArea).text = DEFAULT_SAMPLE_INI
            self._mark_selected("wiz-mode-ini", "wiz-mode-ini", "wiz-mode-ip")
            self._rebuild_wizard_role_and_overrides()
            self._refresh_wizard_preview()
        elif bid == "wiz-mode-ip":
            self.wizard_inv_mode = "quick_ip"
            self.query_one("#inv-textarea", TextArea).text = "web-01=10.20.1.11\nweb-02=10.20.1.12\ndb-01=10.20.2.21"
            self._mark_selected("wiz-mode-ip", "wiz-mode-ini", "wiz-mode-ip")
            self._rebuild_wizard_role_and_overrides()
            self._refresh_wizard_preview()
        elif bid == "wiz-parse-btn":
            self._rebuild_wizard_role_and_overrides()
            self._refresh_wizard_preview()
        elif bid == "wiz-preview-btn":
            self._refresh_wizard_preview()
        elif bid == "wiz-launch-btn":
            self.action_launch_wizard_run()
        elif bid == "live-cancel-btn":
            if self.controller.active_run_id:
                self.controller.request_cancel(self.controller.active_run_id)
                self.notify("已发送协作式取消请求（不假装远端瞬间停止，Section 10.3）")

        # Report buttons
        elif bid == "rep-view-host":
            self.report_perspective = "host"
            self._mark_selected("rep-view-host", "rep-view-host", "rep-view-check")
            self._refresh_report_view()
        elif bid == "rep-view-check":
            self.report_perspective = "check"
            self._mark_selected("rep-view-check", "rep-view-host", "rep-view-check")
            self._refresh_report_view()
        elif bid == "rep-toggle-anomaly":
            self._toggle_report_anomaly_filter()
        elif bid == "rep-view-goss":
            self._show_compiled_goss_modal()
        elif bid == "rep-export-json":
            if self.current_report:
                out_p = self.store.exports_dir / f"{self.current_report.run_id}-report.json"
                out_p.write_text(
                    json.dumps(self.current_report.model_dump(mode="json"), ensure_ascii=False, indent=2),
                    encoding="utf-8",
                )
                self.notify(f"权威 JSON 报告已导出至: {out_p}")
        elif bid == "rep-rerun-full":
            self._rerun_from_report(partial_only=False)
        elif bid == "rep-rerun-partial":
            self._rerun_from_report(partial_only=True)

        # System & Cleanup buttons
        elif bid == "sys-resolve-btn":
            self._open_selected_cleanup_modal()
        elif bid == "sys-refresh-cleanups":
            self._refresh_cleanup_badge()
            self._refresh_system_cleanups()

    def on_select_changed(self, event: Select.Changed) -> None:
        if event.value == Select.BLANK:
            return
        sid = event.select.id or ""
        val = str(event.value)
        if sid == "wb-plan-select":
            if self._plan_key(self.current_plan) == val:
                return
            if self._widget_is_workbench_field(event.select):
                self._wb_dirty = True
            target = next((p for p in self.store.list_plans() if self._plan_key(p) == val), None)
            if target is None:
                return

            def _switch_plan(plan: PlanDocument = target) -> None:
                self.current_plan = plan.model_copy(deep=True)
                self.selected_role_id = plan.roles[0].role_id if plan.roles else ""
                self.selected_instance_id = (
                    plan.roles[0].instances[0].instance_id
                    if (plan.roles and plan.roles[0].instances)
                    else ""
                )
                self._wb_dirty = False
                self._refresh_workbench()

            self._ask_or_move(_switch_plan)
            return
        if not self._wb_loading and self._widget_is_workbench_field(event.select):
            self._wb_dirty = True
            return
        elif sid == "wiz-plan-select":
            if self._plan_key(self.wizard_plan) == val:
                return
            for p in self.store.list_plans():
                if self._plan_key(p) == val:
                    self.wizard_plan = p
                    self._rebuild_wizard_role_and_overrides()
                    self._refresh_wizard_preview()
                    self.query_one("#wiz-plan-info", Static).update(
                        f"当前绑定方案: [bold]{p.plan_id}@{p.version}[/bold] [{p.state.value}] | 摘要: {str(p.digest)[:26]}..."
                    )
                    break
        elif sid == "rr-history-select":
            if self.current_report is not None and self.current_report.run_id == val:
                return
            rep = self.store.get_run_report(val)
            if rep is not None:
                self.current_report = rep
                self._refresh_report_view()
                self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-report"

    def on_input_changed(self, event: Input.Changed) -> None:
        if event.input.id == "sys-cat-search":
            self._refresh_system_catalog(event.value)
            return
        if not self._wb_loading and self._widget_is_workbench_field(event.input):
            self._wb_dirty = True

    def on_checkbox_changed(self, event: Checkbox.Changed) -> None:
        if not self._wb_loading and self._widget_is_workbench_field(event.checkbox):
            self._wb_dirty = True

    def on_data_table_row_highlighted(self, event: DataTable.RowHighlighted) -> None:
        tid = event.data_table.id or ""
        rk = str(event.row_key.value) if event.row_key and event.row_key.value else ""
        if tid == "wb-roles-table" and rk and rk != self.selected_role_id:
            def _switch_role(role_id: str = rk) -> None:
                self.selected_role_id = role_id
                role = self.current_plan.get_role(role_id)
                self.selected_instance_id = (
                    role.instances[0].instance_id if (role and role.instances) else ""
                )
                self._wb_dirty = False
                self._refresh_workbench_instances()
                self._refresh_workbench_params()

            self._ask_or_move(_switch_role)
        elif tid == "wb-inst-table" and rk and rk != self.selected_instance_id:
            def _switch_instance(instance_id: str = rk) -> None:
                self.selected_instance_id = instance_id
                self._wb_dirty = False
                self._refresh_workbench_params()

            self._ask_or_move(_switch_instance)
        elif tid == "report-table" and rk in self._report_row_evidence_map:
            self.query_one("#report-evidence-detail", Static).update(
                self._report_row_evidence_map[rk]
            )
        elif tid == "sys-cleanup-table" and rk:
            rec = next((r for r in self.store.list_cleanup_records() if r.cleanup_id == rk), None)
            if rec:
                self.query_one("#sys-cleanup-detail", Static).update(
                    f"选中项详情: {rec.cleanup_id} | Run={rec.run_id} | 主机={rec.host_name} | 状态={rec.status.value}"
                    + (f" | WAIVED原因: {rec.waived_reason}" if rec.waived_reason else "")
                )

    def on_data_table_row_selected(self, event: DataTable.RowSelected) -> None:
        tid = event.data_table.id or ""
        rk = str(event.row_key.value) if event.row_key and event.row_key.value else ""
        if tid == "home-runs-table" and rk:
            rep = self.store.get_run_report(rk)
            if rep is not None:
                self.current_report = rep
                self._refresh_report_view()
                self.action_nav("view-run-report")
                self.query_one("#rr-switcher", ContentSwitcher).current = "rr-pane-report"
        elif tid == "sys-cleanup-table":
            self._open_selected_cleanup_modal()
