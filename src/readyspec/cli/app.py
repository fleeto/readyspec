"""ReadySpec V1 CLI (`readyspec tui | plan | template | run | report | recovery`)."""

from __future__ import annotations

import asyncio
import json
import sys
from pathlib import Path
from typing import Annotated

import typer
from rich.console import Console
from rich.table import Table

from readyspec.compiler.goss_compiler import CompilationConflictError
from readyspec.compiler.resolver import ParameterResolutionError
from readyspec.constants import ExitCode, PlanState, RunScope
from readyspec.domain.models import (
    ParameterOverrideRule,
    PlanDocument,
    RunBinding,
)
from readyspec.domain.safe_yaml import SafeYamlError, load_safe_yaml_file
from readyspec.inventory.parser import (
    InventoryParseError,
    auto_suggest_role_mapping,
    parse_inventory_auto,
)
from readyspec.normalizer.control_checks import derive_cli_exit_code
from readyspec.registry.builtin_catalog import (
    get_catalog_groups,
    search_templates,
)
from readyspec.registry.workspace_store import PlanLifecycleError, WorkspaceStore
from readyspec.service.controller import ControllerSecurityError, RunController
from readyspec.tui.terminal_guard import protect_terminal_flow_control

app = typer.Typer(
    name="readyspec",
    help="ReadySpec V1 — 通用 Linux 业务软件部署前环境检查编写、执行与报告工具",
    no_args_is_help=True,
    add_completion=False,
)
plan_app = typer.Typer(help="方案管理（静态校验 / 发布 / 自包含导入导出，零 SSH）", no_args_is_help=True)
template_app = typer.Typer(help="内置 9 大领域 54 组目录与 40 项 P0 模板查询", no_args_is_help=True)
run_app = typer.Typer(help="执行前静态预览与非交互 CI 部署门禁执行", no_args_is_help=True)
report_app = typer.Typer(help="历史运行记录查询与权威 JSON 报告导出", no_args_is_help=True)
recovery_app = typer.Typer(help="受控临时目录与崩溃恢复待办管理（三重指纹验证）", no_args_is_help=True)

app.add_typer(plan_app, name="plan")
app.add_typer(template_app, name="template")
app.add_typer(run_app, name="run")
app.add_typer(report_app, name="report")
app.add_typer(recovery_app, name="recovery")

console = Console(highlight=False)


def _get_store(workspace: Path | None, *, seed_demo: bool = False) -> WorkspaceStore:
    return WorkspaceStore(workspace, seed_demo=seed_demo)


@app.command("tui")
def cmd_tui(
    workspace: Annotated[
        Path | None,
        typer.Option("--workspace", "-w", help="指定本地工作区目录（默认 ~/.local/share/readyspec/workspace）"),
    ] = None,
    no_seed: Annotated[
        bool,
        typer.Option("--no-seed", help="空工作区时不预置演示方案与样例记录"),
    ] = False,
) -> None:
    """启动四幕全流程交互式终端界面 (Textual TUI)。"""
    from readyspec.tui.app import ReadySpecApp

    store = _get_store(workspace, seed_demo=not no_seed)
    lock = store.create_lock()
    try:
        lock.acquire()
    except RuntimeError as exc:
        console.print(f"[red]{exc}[/red]")
        raise typer.Exit(int(ExitCode.INFRA_ERROR)) from exc

    try:
        with protect_terminal_flow_control():
            tui = ReadySpecApp(store=store)
            tui.run()
    finally:
        lock.release()


# ------------------------------------------------------------------
# `readyspec plan ...`
# ------------------------------------------------------------------
@plan_app.command("list")
def plan_list(
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
    seed_demo: Annotated[bool, typer.Option("--seed-demo")] = False,
) -> None:
    """列出工作区内的草稿与正式已发布方案。"""
    store = _get_store(workspace, seed_demo=seed_demo)
    plans = store.list_plans()
    tbl = Table(title=f"工作区方案列表 ({store.root})")
    tbl.add_column("方案 ID")
    tbl.add_column("版本")
    tbl.add_column("状态")
    tbl.add_column("名称")
    tbl.add_column("角色数")
    tbl.add_column("语义摘要 (RFC 8785)")
    for p in plans:
        tbl.add_row(
            p.plan_id,
            p.version,
            p.state.value,
            p.name,
            str(len(p.roles)),
            (p.digest or "")[:28] + "...",
        )
    console.print(tbl)


@plan_app.command("validate")
def plan_validate(
    plan_file: Annotated[Path, typer.Argument(help="待静态校验的方案 YAML 文件路径")],
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """静态校验方案 YAML（Schema、重复键、参数约束与特权可信性；不发起 SSH）。"""
    store = _get_store(workspace)
    try:
        raw = load_safe_yaml_file(plan_file)
        plan = PlanDocument.model_validate(raw)
    except (SafeYamlError, Exception) as exc:
        console.print(f"[red]PLAN_ERROR: {exc}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc

    diags = store.validate_plan(plan)
    errors = [d for d in diags if d.severity == "ERROR"]
    for d in diags:
        color = "red" if d.severity == "ERROR" else "yellow"
        console.print(f"[{color}][{d.severity}] {d.code}: {d.message}[/{color}]")
    if errors:
        raise typer.Exit(int(ExitCode.PLAN_ERROR))
    console.print(
        f"[green]VALID: {plan.plan_id}@{plan.version} | digest={plan.compute_semantic_digest()}[/green]"
    )


@plan_app.command("release")
def plan_release(
    plan_id: Annotated[str, typer.Argument(help="工作区方案 ID")],
    version: Annotated[str | None, typer.Option("--version", "-v", help="发布的正式版本号")] = None,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """将工作区方案静态校验并发布为不可变 RELEASED 版本。"""
    store = _get_store(workspace)
    plan = store.get_plan(plan_id, prefer_released=False)
    if plan is None:
        console.print(f"[red]找不到方案: {plan_id}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR))
    try:
        released = store.release_plan(plan, version=version)
    except PlanLifecycleError as exc:
        console.print(f"[red][{exc.code}] {exc.message}[/red]")
        for d in exc.diagnostics:
            console.print(f"  - [{d.code}] {d.message}")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc
    console.print(
        f"[green]RELEASED: {released.plan_id}@{released.version} | digest={released.digest}[/green]"
    )


@plan_app.command("export")
def plan_export(
    plan_id: Annotated[str, typer.Argument(help="方案 ID")],
    version: Annotated[str | None, typer.Option("--version", "-v")] = None,
    out: Annotated[Path | None, typer.Option("--out", "-o", help="导出文件路径（不传则打印至 stdout）")] = None,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """导出自包含单文件方案 YAML（含锁定模板快照，不含任何凭据或报告）。"""
    store = _get_store(workspace)
    plan = store.get_plan(plan_id, version=version, prefer_released=True)
    if plan is None:
        console.print(f"[red]找不到方案: {plan_id}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR))
    yaml_text = store.export_plan_yaml(plan, out)
    if out is None:
        sys.stdout.write(yaml_text)
    else:
        console.print(f"[green]已导出至 {out} (digest={plan.digest})[/green]")


@plan_app.command("import")
def plan_import(
    source: Annotated[str, typer.Argument(help="YAML 文件路径，或 '-' 从标准输入读取粘贴文本")],
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """安全导入外部方案 YAML（校验重复键、Schema、摘要冲突与特权信任，零执行）。"""
    store = _get_store(workspace)
    content = sys.stdin.read() if source == "-" else Path(source).read_bytes()
    try:
        imported = store.import_plan_yaml(content, save_to_workspace=True)
    except PlanLifecycleError as exc:
        console.print(f"[red][{exc.code}] {exc.message}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc
    console.print(
        f"[green]IMPORTED: {imported.plan_id}@{imported.version} [{imported.state.value}] | digest={imported.digest}[/green]"
    )


# ------------------------------------------------------------------
# `readyspec template ...`
# ------------------------------------------------------------------
@template_app.command("list")
def template_list(
    query: Annotated[str, typer.Option("--query", "-q", help="中英文关键词过滤")] = "",
    domain: Annotated[str | None, typer.Option("--domain", "-d", help="按领域代码筛选 (H/O/S/N/R/A/D/L/P)")] = None,
    catalog: Annotated[bool, typer.Option("--catalog", help="展示完整 54 个检查组目录（含 14 项 Planned）")] = False,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """查询 9 大领域 54 组目录及 40 项可执行 P0 模板。"""
    store = _get_store(workspace)
    if catalog:
        groups = get_catalog_groups()
        tbl = Table(title=f"ReadySpec 九大领域 · {len(groups)} 检查组目录")
        tbl.add_column("领域")
        tbl.add_column("组号")
        tbl.add_column("中文名")
        tbl.add_column("成熟度")
        tbl.add_column("关联模板")
        for g in groups:
            if domain and g.domain_code != domain.upper():
                continue
            if query and query.lower() not in f"{g.group_code} {g.group_name_zh} {g.group_name_en}".lower():
                continue
            tbl.add_row(
                f"{g.domain_code} {g.domain_name_zh}",
                g.group_code,
                g.group_name_zh,
                g.maturity.value,
                g.template_id or "-",
            )
        console.print(tbl)
        return

    avail = list(store.get_all_available_templates().values())
    filtered = search_templates(
        avail, query, domain_code=domain.upper() if domain else None, include_control=True
    )
    tbl = Table(title=f"可执行模板列表 ({len(filtered)} 项)")
    tbl.add_column("ID")
    tbl.add_column("名称")
    tbl.add_column("来源")
    tbl.add_column("权限")
    tbl.add_column("证据深度")
    for t in filtered:
        tbl.add_row(t.template_id, t.name_zh, t.source.value, t.privilege_group.value, t.evidence_depth.value)
    console.print(tbl)


# ------------------------------------------------------------------
# `readyspec run preview | submit`
# ------------------------------------------------------------------
def _build_cli_binding_and_plan(
    store: WorkspaceStore,
    *,
    plan_ref: str,
    inventory_ref: str,
    ssh_user: str,
    role_maps: list[str],
    overrides: list[str],
    approve_privileged: str,
    approve_root_ssh: bool,
    partial_hosts: str,
    profile: str,
) -> tuple[PlanDocument, RunBinding]:
    p_path = Path(plan_ref)
    if p_path.exists() and p_path.is_file():
        plan = store.import_plan_yaml(p_path.read_bytes(), save_to_workspace=False)
    else:
        if "@" in plan_ref:
            pid, pver = plan_ref.split("@", 1)
            plan = store.get_plan(pid, version=pver)
        else:
            plan = store.get_plan(plan_ref, prefer_released=True)
        if plan is None:
            raise PlanLifecycleError("PLAN_NOT_FOUND", f"找不到方案 '{plan_ref}'")

    inv_path = Path(inventory_ref)
    inv_text = (
        inv_path.read_text(encoding="utf-8")
        if (inv_path.exists() and inv_path.is_file())
        else inventory_ref
    )
    inv = parse_inventory_auto(inv_text, default_user=ssh_user)

    role_to_hosts = auto_suggest_role_mapping(plan, inv)
    for rm in role_maps:
        if "=" not in rm:
            raise PlanLifecycleError("INVALID_ROLE_MAP", f"--role-map 格式应为 role=h1,h2: {rm}")
        r_id, h_csv = rm.split("=", 1)
        role_to_hosts[r_id.strip()] = [h.strip() for h in h_csv.split(",") if h.strip()]

    templates = store.get_all_available_templates(plan)
    parsed_overrides: list[ParameterOverrideRule] = []
    for ov in overrides:
        # Format: `[host:<name>|group:<grp>:]role_id.instance_id.param=value`
        if "=" not in ov:
            raise PlanLifecycleError("INVALID_OVERRIDE", f"--override 缺少 '=': {ov}")
        lhs, rhs_str = ov.split("=", 1)
        scope_type = "group"
        scope_target = "all"
        if ":" in lhs:
            st, stgt, lhs = lhs.split(":", 2)
            scope_type = st
            scope_target = stgt
        parts = lhs.split(".")
        if len(parts) != 3:
            raise PlanLifecycleError(
                "INVALID_OVERRIDE",
                f"--override 键格式应为 role_id.instance_id.param=val: {ov}",
            )
        r_id, inst_id, p_name = parts
        # Type-coerce value using parameter spec if found
        val: object = rhs_str
        role_obj = plan.get_role(r_id)
        if role_obj:
            inst_obj = next((i for i in role_obj.instances if i.instance_id == inst_id), None)
            if inst_obj and inst_obj.template_id in templates:
                p_spec = next(
                    (p for p in templates[inst_obj.template_id].parameters if p.name == p_name),
                    None,
                )
                if p_spec:
                    if p_spec.param_type.value in ("integer", "port"):
                        val = int(rhs_str)
                    elif p_spec.param_type.value == "number":
                        val = float(rhs_str)
                    elif p_spec.param_type.value == "boolean":
                        val = rhs_str.lower() in ("true", "1", "yes")
        parsed_overrides.append(
            ParameterOverrideRule(
                scope_type=scope_type,
                scope_target=scope_target,
                role_id=r_id,
                instance_id=inst_id,
                param_name=p_name,
                value=val,
            )
        )

    priv_list = [x.strip() for x in approve_privileged.split(",") if x.strip()]
    part_list = [x.strip() for x in partial_hosts.split(",") if x.strip()]
    scope = RunScope.PARTIAL_SCOPE if part_list else RunScope.FULL

    binding = RunBinding(
        plan_id=plan.plan_id,
        plan_version=plan.version,
        plan_state=plan.state,
        plan_digest=plan.digest or plan.compute_semantic_digest(),
        inventory=inv,
        role_to_hosts=role_to_hosts,
        runtime_overrides=parsed_overrides,
        approved_privileged_templates=priv_list,
        approve_root_ssh=approve_root_ssh,
        run_scope=scope,
        selected_hosts_filter=part_list,
        simulation_profile=profile,
    )
    return plan, binding


@run_app.command("preview")
def run_preview(
    plan: Annotated[str, typer.Option("--plan", "-p", help="方案文件路径或工作区 plan_id[@version]")],
    inventory: Annotated[str, typer.Option("--inventory", "-i", help="Inventory 文件路径或快速 IP 列表")],
    ssh_user: Annotated[str, typer.Option("--ssh-user", help="默认 SSH 用户名")] = "ops",
    role_map: Annotated[list[str] | None, typer.Option("--role-map", help="角色映射 role=h1,h2")] = None,
    override: Annotated[list[str] | None, typer.Option("--override", help="现场覆盖 role.inst.param=val")] = None,
    approve_privileged: Annotated[str, typer.Option("--approve-privileged", help="批准特权模板 ID 列表，如 A03")] = "",
    approve_root_ssh: Annotated[bool, typer.Option("--approve-root-ssh", help="显式批准 root SSH 风险")] = False,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """静态运行前预览（报告预期主机数、候选检查数、特权/root 风险，绝不连接 SSH）。"""
    store = _get_store(workspace)
    controller = RunController(store)
    try:
        plan_doc, binding = _build_cli_binding_and_plan(
            store,
            plan_ref=plan,
            inventory_ref=inventory,
            ssh_user=ssh_user,
            role_maps=role_map or [],
            overrides=override or [],
            approve_privileged=approve_privileged,
            approve_root_ssh=approve_root_ssh,
            partial_hosts="",
            profile="mixed_realistic",
        )
        preview = controller.preview_run(plan_doc, binding)
    except (PlanLifecycleError, InventoryParseError, ParameterResolutionError) as exc:
        console.print(f"[red]PREVIEW_ERROR: {exc}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc

    console.print_json(json.dumps(preview.model_dump(mode="json"), ensure_ascii=False))
    if any(d.severity == "ERROR" for d in preview.diagnostics):
        raise typer.Exit(int(ExitCode.PLAN_ERROR))


@run_app.command("submit")
def run_submit(
    plan: Annotated[str, typer.Option("--plan", "-p", help="正式 RELEASED 方案文件或 plan_id[@version]")],
    inventory: Annotated[str, typer.Option("--inventory", "-i", help="Inventory 文件或快速 IP 列表")],
    ssh_user: Annotated[str, typer.Option("--ssh-user", help="默认 SSH 用户")] = "ops",
    role_map: Annotated[list[str] | None, typer.Option("--role-map", help="角色绑定 role=h1,h2")] = None,
    override: Annotated[list[str] | None, typer.Option("--override", help="现场参数覆盖 role.inst.param=val")] = None,
    expected_plan_digest: Annotated[str | None, typer.Option("--expected-plan-digest", help="CI 强制锁定的方案摘要 sha256:...")] = None,
    approve_privileged: Annotated[str, typer.Option("--approve-privileged", help="显式预授权内置特权模板白名单，如 A03,L02")] = "",
    approve_root_ssh: Annotated[bool, typer.Option("--approve-root-ssh", help="显式批准高风险 root SSH 连接")] = False,
    partial_hosts: Annotated[str, typer.Option("--partial-hosts", help="局部快测主机列表（强制标记 PARTIAL_SCOPE，不返回退出码 0）")] = "",
    profile: Annotated[str, typer.Option("--profile", help="仿真器环境画像 (all_ready / mixed_realistic / log_gap_and_missing_tool / interrupted_cleanup)")] = "all_ready",
    report_out: Annotated[Path | None, typer.Option("--report-out", "-o", help="将最终权威 JSON 报告写入指定路径")] = None,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """非交互执行部署前检查并按锁定契约返回门禁退出码 (0 / 10~15)。"""
    store = _get_store(workspace)
    controller = RunController(store)

    try:
        plan_doc, binding = _build_cli_binding_and_plan(
            store,
            plan_ref=plan,
            inventory_ref=inventory,
            ssh_user=ssh_user,
            role_maps=role_map or [],
            overrides=override or [],
            approve_privileged=approve_privileged,
            approve_root_ssh=approve_root_ssh,
            partial_hosts=partial_hosts,
            profile=profile,
        )
    except (PlanLifecycleError, InventoryParseError) as exc:
        console.print(f"[red]PLAN_OR_INVENTORY_ERROR: {exc}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc

    # Formal CLI gate rejects raw DRAFT plans (must be RELEASED or explicit TRIAL_SNAPSHOT)
    if plan_doc.state == PlanState.DRAFT:
        console.print("[red]UNAPPROVED_DRAFT: 草稿方案未发布为 RELEASED，禁止在非交互 CLI 直接执行。[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR))

    try:
        report = asyncio.run(
            controller.execute_run(
                plan_doc,
                binding,
                expected_plan_digest=expected_plan_digest,
            )
        )
    except ControllerSecurityError as exc:
        console.print(f"[red][{exc.code}] {exc.message}[/red]")
        raise typer.Exit(int(ExitCode.AUTH_ERROR)) from exc
    except (CompilationConflictError, ParameterResolutionError) as exc:
        console.print(f"[red][{exc.code}] {exc.message}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc
    except Exception as exc:
        console.print(f"[red]INFRA_ERROR: {exc}[/red]")
        raise typer.Exit(int(ExitCode.INFRA_ERROR)) from exc

    if report_out is not None:
        report_out.parent.mkdir(parents=True, exist_ok=True)
        report_out.write_text(
            json.dumps(report.model_dump(mode="json"), ensure_ascii=False, indent=2),
            encoding="utf-8",
        )

    exit_code = derive_cli_exit_code(
        report.run_status, report.readiness, report.gate_eligible
    )
    console.print(
        f"RUN_ID={report.run_id} BACKEND={report.execution_backend} "
        f"SCOPE={report.run_scope.value} STATUS={report.run_status.value} "
        f"READINESS={report.readiness.value} GATE_ELIGIBLE={report.gate_eligible} "
        f"EXIT_CODE={int(exit_code)}"
    )
    console.print(f"REASON: {report.readiness_reason}")
    raise typer.Exit(int(exit_code))


# ------------------------------------------------------------------
# `readyspec report ...` & `readyspec recovery ...`
# ------------------------------------------------------------------
@report_app.command("list")
def report_list(
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """列出历史运行报告。"""
    store = _get_store(workspace)
    tbl = Table(title="历史运行报告")
    tbl.add_column("Run ID")
    tbl.add_column("方案")
    tbl.add_column("范围")
    tbl.add_column("Readiness")
    tbl.add_column("门禁放行")
    for r in store.list_run_reports():
        tbl.add_row(
            r.run_id,
            f"{r.plan_id}@{r.plan_version}",
            r.run_scope.value,
            r.readiness.value,
            str(r.gate_eligible),
        )
    console.print(tbl)


@report_app.command("export")
def report_export(
    run_id: Annotated[str, typer.Argument(help="Run ID")],
    out: Annotated[Path | None, typer.Option("--out", "-o")] = None,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """导出指定 Run 的版本化 JSON 报告。"""
    store = _get_store(workspace)
    rep = store.get_run_report(run_id)
    if rep is None:
        console.print(f"[red]找不到运行报告: {run_id}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR))
    txt = json.dumps(rep.model_dump(mode="json"), ensure_ascii=False, indent=2)
    if out is None:
        sys.stdout.write(txt + "\n")
    else:
        out.write_text(txt, encoding="utf-8")
        console.print(f"[green]已导出报告至 {out}[/green]")


@recovery_app.command("list")
def recovery_list(
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """查询受控临时目录与未结案残留进程恢复记录。"""
    store = _get_store(workspace)
    tbl = Table(title="恢复与清理记录")
    tbl.add_column("Cleanup ID")
    tbl.add_column("Run ID")
    tbl.add_column("主机")
    tbl.add_column("状态")
    tbl.add_column("存活进程")
    tbl.add_column("受控路径")
    for r in store.list_cleanup_records():
        tbl.add_row(
            r.cleanup_id,
            r.run_id,
            r.host_name,
            r.status.value,
            f"PID={r.process_pid}" if r.live_process_detected else "无",
            r.remote_dir,
        )
    console.print(tbl)


@recovery_app.command("resolve")
def recovery_resolve(
    cleanup_id: Annotated[str, typer.Argument(help="待处置的 Cleanup ID")],
    confirm_kill: Annotated[bool, typer.Option("--confirm-kill", help="显式确认终止匹配三重指纹的远端存活进程")] = False,
    waive_reason: Annotated[str | None, typer.Option("--waive-reason", help="放弃自动清理并留存永久 WAIVED 审计的原因")] = None,
    workspace: Annotated[Path | None, typer.Option("--workspace", "-w")] = None,
) -> None:
    """处置待清理记录（执行受控清理或标记 WAIVED）。"""
    store = _get_store(workspace)
    controller = RunController(store)
    try:
        updated = asyncio.run(
            controller.resolve_cleanup_item(
                cleanup_id,
                confirm_kill_live_process=confirm_kill,
                waive_reason=waive_reason,
            )
        )
    except PermissionError as exc:
        console.print(f"[red]CONFIRMATION_REQUIRED: {exc}[/red]")
        raise typer.Exit(int(ExitCode.AUTH_ERROR)) from exc
    except Exception as exc:
        console.print(f"[red]RECOVERY_ERROR: {exc}[/red]")
        raise typer.Exit(int(ExitCode.PLAN_ERROR)) from exc
    console.print(
        f"[green]RESOLVED: {updated.cleanup_id} -> {updated.status.value}[/green]"
    )


def main() -> None:
    app()


if __name__ == "__main__":
    main()
