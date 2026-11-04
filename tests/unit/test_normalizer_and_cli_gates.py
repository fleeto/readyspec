"""Unit & CLI contract tests for Shell evaluator, Readiness gates, exit codes, and Cleanup safety."""

from __future__ import annotations

from pathlib import Path

from typer.testing import CliRunner

from readyspec.cli.app import app
from readyspec.constants import CheckStatus, ExitCode, ReadinessStatus, RunScope
from readyspec.domain.models import (
    NumericOperator,
    ShellAssertionMode,
    ShellAssertionSpec,
)
from readyspec.inventory.parser import parse_ansible_ini
from readyspec.normalizer.shell_evaluator import evaluate_custom_shell_output
from readyspec.registry.workspace_store import WorkspaceStore
from readyspec.service.controller import RunController

runner = CliRunner()


def test_shell_evaluator_basic_and_custom_protocol_v1() -> None:
    # Basic numeric assertion + NaN rejection
    num_spec = ShellAssertionSpec(
        mode=ShellAssertionMode.BASIC,
        numeric_op=NumericOperator.GE,
        numeric_threshold=65535,
    )
    assert evaluate_custom_shell_output(num_spec, exit_code=0, stdout="131072\n").status == CheckStatus.PASS
    assert evaluate_custom_shell_output(num_spec, exit_code=0, stdout="1024\n").status == CheckStatus.FAIL
    assert evaluate_custom_shell_output(num_spec, exit_code=0, stdout="not_a_number").status == CheckStatus.ERROR
    assert evaluate_custom_shell_output(num_spec, exit_code=127, stdout="").status == CheckStatus.ERROR

    # Custom Result Protocol v1: valid PASS, valid FAIL, and forged READY/UNSUPPORTED rejection (REQ-SEC-13)
    proto_spec = ShellAssertionSpec(mode=ShellAssertionMode.CUSTOM_RESULT_V1)
    ok_res = evaluate_custom_shell_output(
        proto_spec,
        exit_code=0,
        stdout='{"protocol":"readyspec.custom_result/v1","status":"FAIL","summary":"配置缺失"}',
    )
    assert ok_res.status == CheckStatus.FAIL

    forged = evaluate_custom_shell_output(
        proto_spec,
        exit_code=0,
        stdout='{"protocol":"readyspec.custom_result/v1","status":"UNSUPPORTED","summary":"forged"}',
    )
    assert forged.status == CheckStatus.ERROR

    inconsistent = evaluate_custom_shell_output(
        proto_spec,
        exit_code=2,
        stdout='{"protocol":"readyspec.custom_result/v1","status":"PASS","summary":"contradiction"}',
    )
    assert inconsistent.status == CheckStatus.ERROR


async def test_vault_ephemeral_burn_and_trial_vs_partial_gates(tmp_path: Path) -> None:
    store = WorkspaceStore(tmp_path / "ws", seed_demo=True)
    ctrl = RunController(store)

    # 1. Draft TRIAL_SNAPSHOT can run, burns secret on finish, and NEVER gets gate_eligible=True
    draft = store.get_plan("payment-gateway", prefer_released=False)
    assert draft is not None
    snapshot = store.create_trial_snapshot(draft)
    inv = parse_ansible_ini("[gateway]\ngw-01 ansible_host=10.10.0.1\n")
    tok = ctrl.vault.put_secret("super-secret-password")
    assert ctrl.vault.active_count == 1

    from readyspec.domain.models import RunBinding

    trial_binding = RunBinding(
        plan_id=snapshot.plan_id,
        plan_version=snapshot.version,
        plan_state=snapshot.state,
        plan_digest=snapshot.digest or "",
        inventory=inv,
        role_to_hosts={"gateway": ["gw-01"]},
        simulation_profile="all_ready",
        credential_token=tok,
    )
    rep = await ctrl.execute_run(snapshot, trial_binding)
    assert ctrl.vault.active_count == 0
    assert rep.readiness == ReadinessStatus.READY
    assert rep.gate_eligible is False

    # 2. PARTIAL_SCOPE fault-triage re-run NEVER outputs global READY even if all tested hosts pass!
    released = store.get_plan("order-platform", "1.0.0")
    assert released is not None
    full_inv = parse_ansible_ini(
        "[web]\nw1 ansible_host=10.0.1.1\nw2 ansible_host=10.0.1.2\n[db]\nd1 ansible_host=10.0.2.1\n"
    )
    partial_binding = RunBinding(
        plan_id=released.plan_id,
        plan_version=released.version,
        plan_state=released.state,
        plan_digest=released.digest or "",
        inventory=full_inv,
        role_to_hosts={"web": ["w1", "w2"], "db": ["d1"]},
        approved_privileged_templates=["A03"],
        run_scope=RunScope.PARTIAL_SCOPE,
        selected_hosts_filter=["d1"],
        simulation_profile="all_ready",
    )
    partial_rep = await ctrl.execute_run(released, partial_binding)
    assert partial_rep.run_scope == RunScope.PARTIAL_SCOPE
    assert partial_rep.readiness == ReadinessStatus.UNKNOWN
    assert partial_rep.gate_eligible is False


def test_cli_exit_codes_and_root_ssh_and_cleanup_contracts(tmp_path: Path) -> None:
    ws_dir = tmp_path / "cli_ws"
    WorkspaceStore(ws_dir, seed_demo=True)
    inv_file = tmp_path / "hosts.ini"
    inv_file.write_text(
        "[web]\nw1 ansible_host=10.0.1.11\nw2 ansible_host=10.0.1.12\n[db]\nd1 ansible_host=10.0.2.21\n",
        encoding="utf-8",
    )

    # No-arg CLI prints help cleanly (TODO-011)
    res_help = runner.invoke(app, [])
    assert "tui" in res_help.stdout and "plan" in res_help.stdout

    # 1. Full RELEASED + all_ready + approved A03 -> ExitCode.READY (0)
    res_ok = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--approve-privileged",
            "A03",
            "--profile",
            "all_ready",
        ],
    )
    assert res_ok.exit_code == int(ExitCode.READY), res_ok.stdout

    # 2. mixed_realistic (db disk 42.5 GiB < 100 GiB) -> ExitCode.NOT_READY (10)
    res_fail = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--approve-privileged",
            "A03",
            "--profile",
            "mixed_realistic",
        ],
    )
    assert res_fail.exit_code == int(ExitCode.NOT_READY), res_fail.stdout

    # 3. Runtime override (`db.db_data_space.path=/data2`) fixes the environmental difference -> ExitCode.READY (0)!
    res_ov = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--approve-privileged",
            "A03",
            "--override",
            "db.db_data_space.path=/data2",
            "--profile",
            "mixed_realistic",
        ],
    )
    assert res_ov.exit_code == int(ExitCode.READY), res_ov.stdout

    # 4. --partial-hosts d1 -> PARTIAL_SCOPE -> ExitCode.UNKNOWN (11), NEVER 0!
    res_part = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--approve-privileged",
            "A03",
            "--partial-hosts",
            "d1",
            "--profile",
            "all_ready",
        ],
    )
    assert res_part.exit_code == int(ExitCode.UNKNOWN), res_part.stdout

    # 5. Unapproved root SSH -> ExitCode.AUTH_ERROR (15); approved --approve-root-ssh -> ExitCode.READY (0) (CHG-001)
    res_root_deny = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--ssh-user",
            "root",
            "--approve-privileged",
            "A03",
            "--profile",
            "all_ready",
        ],
    )
    assert res_root_deny.exit_code == int(ExitCode.AUTH_ERROR)

    report_json = tmp_path / "root_approved_report.json"
    res_root_ok = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--ssh-user",
            "root",
            "--approve-root-ssh",
            "--approve-privileged",
            "A03",
            "--profile",
            "all_ready",
            "--report-out",
            str(report_json),
        ],
    )
    assert res_root_ok.exit_code == int(ExitCode.READY)
    assert '"high_risk_root_audit": true' in report_json.read_text(encoding="utf-8")

    # 6. Wrong --expected-plan-digest -> ExitCode.AUTH_ERROR (15) (TODO-018)
    res_bad_dig = runner.invoke(
        app,
        [
            "run",
            "submit",
            "--workspace",
            str(ws_dir),
            "--plan",
            "order-platform@1.0.0",
            "--inventory",
            str(inv_file),
            "--expected-plan-digest",
            "sha256:0000",
        ],
    )
    assert res_bad_dig.exit_code == int(ExitCode.AUTH_ERROR)

    # 7. Cleanup record with live PID refuses cleanup without --confirm-kill, succeeds with --confirm-kill (TODO-007)
    res_cln_deny = runner.invoke(
        app,
        ["recovery", "resolve", "cln-demo-001", "--workspace", str(ws_dir)],
    )
    assert res_cln_deny.exit_code == int(ExitCode.AUTH_ERROR)

    res_cln_ok = runner.invoke(
        app,
        ["recovery", "resolve", "cln-demo-001", "--confirm-kill", "--workspace", str(ws_dir)],
    )
    assert res_cln_ok.exit_code == 0
