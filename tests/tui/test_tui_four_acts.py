"""Headless Textual Pilot tests verifying the 4-Act TUI across 80x24, 110x30, and 140x40."""

from __future__ import annotations

from pathlib import Path

import pytest
from textual import events
from textual.geometry import Offset
from textual.widgets import Checkbox, ContentSwitcher, DataTable, Input, Select, Static

from readyspec.constants import CleanupStatus, ReadinessStatus, RunScope
from readyspec.execution.mock_executor import MockRemoteExecutor
from readyspec.registry.workspace_store import WorkspaceStore
from readyspec.service.controller import RunController
from readyspec.tui.app import ReadySpecApp


@pytest.mark.parametrize("term_size", [(140, 40), (110, 30), (80, 24)])
async def test_tui_responsive_layouts_and_input_hotkey_isolation(
    tmp_path: Path, term_size: tuple[int, int]
) -> None:
    store = WorkspaceStore(tmp_path / f"ws_{term_size[0]}", seed_demo=True)
    ctrl = RunController(store, executor=MockRemoteExecutor(step_delay_sec=0.0))
    app = ReadySpecApp(store=store, controller=ctrl)

    async with app.run_test(size=term_size) as pilot:
        switcher = app.query_one("#main-switcher", ContentSwitcher)
        assert switcher.current == "view-home"

        # Navigate across all 4 top-level views via single-key shortcuts
        await pilot.press("2")
        assert switcher.current == "view-workbench"

        # Verify responsive columns match terminal width
        roles_col = app.query_one("#wb-col-roles")
        inst_col = app.query_one("#wb-col-instances")
        params_col = app.query_one("#wb-col-params")
        if term_size[0] >= 140:
            assert not roles_col.has_class("hidden-pane")
            assert not inst_col.has_class("hidden-pane")
            assert not params_col.has_class("hidden-pane")
        elif term_size[0] >= 100:
            assert params_col.has_class("hidden-pane")
        else:
            assert roles_col.has_class("hidden-pane")
            assert not inst_col.has_class("hidden-pane")
            assert params_col.has_class("hidden-pane")

        await pilot.press("n")
        await pilot.pause()
        role_input = app.screen.query_one("#ht-id", Input)
        role_input.focus()
        await pilot.pause()
        await pilot.press("1", "3", "4", "a", "c")
        assert switcher.current == "view-workbench"
        assert role_input.value == "134ac"


async def test_workbench_param_column_stays_on_screen_and_enum_is_select(tmp_path: Path) -> None:
    store = WorkspaceStore(tmp_path / "ws_params", seed_demo=True)
    app = ReadySpecApp(store=store, controller=RunController(store, executor=MockRemoteExecutor(step_delay_sec=0.0)))

    async with app.run_test(size=(140, 40)) as pilot:
        await pilot.press("2")
        await pilot.pause()
        table = app.query_one("#wb-inst-table", DataTable)
        n02_key = next(key for key in table.rows if str(table.get_row(key)[1]).startswith("N02"))
        table.move_cursor(row=table.get_row_index(n02_key))
        await pilot.pause()

        col = app.query_one("#wb-col-params")
        assert col.region.right <= app.size.width
        for label in col.query("Label"):
            assert label.region.right <= col.region.right

        protocol = next(widget for widget in col.query(Select) if widget.value == "tcp")
        protocol.action_show_overlay()
        await pilot.pause()
        overlay = protocol.query_one("SelectOverlay")
        assert overlay.display is True
        prompts = [str(option.prompt) for option in overlay.options]
        assert prompts == ["tcp", "udp"]
        assert overlay.region.right <= app.size.width
        assert overlay.region.bottom <= app.size.height


async def test_cpu_arch_choices_are_visible_without_popup(tmp_path: Path) -> None:
    store = WorkspaceStore(tmp_path / "ws_arch", seed_demo=True)
    app = ReadySpecApp(store=store, controller=RunController(store, executor=MockRemoteExecutor(step_delay_sec=0.0)))

    async with app.run_test(size=(140, 40)) as pilot:
        await pilot.press("2")
        await pilot.pause()
        plan_sel = app.query_one("#wb-plan-select", Select)
        released = next(plan for plan in store.list_plans() if plan.plan_id == "order-platform")
        plan_sel.value = f"{released.state.value}:{released.plan_id}:{released.version}"
        await pilot.pause()
        table = app.query_one("#wb-inst-table", DataTable)
        arch_key = next(key for key in table.rows if str(key.value) == "web_cpu_arch")
        table.move_cursor(row=table.get_row_index(arch_key))
        await pilot.pause()

        labels = [box.label for box in app.query_one("#wb-col-params").query(Checkbox)]
        assert "x86_64" in labels
        assert "aarch64" in labels
        checked = {box.label for box in app.query_one("#wb-col-params").query(Checkbox) if box.value}
        assert "x86_64" in checked
        assert "aarch64" not in checked


async def test_delete_instance_message_is_complete_and_toast_click_does_not_crash(
    tmp_path: Path,
) -> None:
    store = WorkspaceStore(tmp_path / "ws_del", seed_demo=True)
    app = ReadySpecApp(store=store, controller=RunController(store, executor=MockRemoteExecutor(step_delay_sec=0.0)))

    async with app.run_test(size=(140, 40)) as pilot:
        await pilot.press("2")
        await pilot.pause()
        deleted_id = app.selected_instance_id
        await pilot.press("x")
        await pilot.pause()

        status = str(app.query_one("#wb-diag-bar").render())
        assert deleted_id in status
        assert "撤销" in status

        orphan = Static("toast")
        app.screen.get_widget_and_offset_at = lambda _x, _y: (orphan, Offset(0, 0))  # type: ignore[method-assign]
        app.screen._forward_event(
            events.MouseDown(None, 124, 36, 0, 0, 1, False, False, False, 124, 36)
        )
        await pilot.pause()
        assert app._exception is None


async def test_tui_end_to_end_workbench_wizard_report_and_cleanup(tmp_path: Path) -> None:
    store = WorkspaceStore(tmp_path / "ws_e2e", seed_demo=True)
    ctrl = RunController(store, executor=MockRemoteExecutor(step_delay_sec=0.0))
    app = ReadySpecApp(store=store, controller=ctrl)

    async with app.run_test(size=(140, 40)) as pilot:
        # 1. Jump to Workbench (Act 2), open Template Picker (`a`), add a check, and save draft (`ctrl+s`)
        await pilot.press("2")
        await pilot.press("a")
        await pilot.pause()
        await pilot.click("#picker-confirm")
        await pilot.pause()
        await pilot.press("ctrl+s")
        await pilot.pause()

        # 2. Jump to Run Wizard (Act 3), scroll launch button into viewport, and launch execution
        await pilot.press("3")
        await pilot.pause()
        app.query_one("#wiz-launch-btn").scroll_visible(animate=False)
        await pilot.pause()
        await pilot.click("#wiz-launch-btn")
        await pilot.pause(0.25)

        assert app.current_report is not None
        first_run_id = app.current_report.run_id
        assert app.current_report.readiness == ReadinessStatus.NOT_READY

        # 3. Toggle perspective (`按检查项视角`), anomaly filter (`f`), and trigger Partial Fault-Triage Re-run (`t`)
        await pilot.click("#rep-view-check")
        await pilot.press("f")
        await pilot.pause()

        # Simulate operator fixing the fault on db-node-01, then running `[t] 仅快测异常主机`
        assert app.last_binding is not None
        app.last_binding.simulation_profile = "all_ready"
        await pilot.press("t")
        await pilot.pause(0.25)
        assert app.current_report.run_id != first_run_id
        assert app.current_report.run_scope == RunScope.PARTIAL_SCOPE
        # Even though the fault host passed, PARTIAL_SCOPE strictly forbids global READY!
        assert app.current_report.readiness == ReadinessStatus.UNKNOWN
        assert app.current_report.gate_eligible is False

        # 4. Jump to System Cleanup Wizard (`4`) and resolve `cln-demo-001` with live-PID confirmation
        await pilot.press("4")
        await pilot.pause()
        await pilot.click("#sys-resolve-btn")
        await pilot.pause()
        # Confirm live process termination checkbox in modal and click clean
        chk = app.screen.query_one("#cln-confirm-kill", Checkbox)
        chk.value = True
        await pilot.click("#cln-do-clean")
        await pilot.pause(0.2)

        resolved_records = {r.cleanup_id: r for r in store.list_cleanup_records()}
        assert resolved_records["cln-demo-001"].status == CleanupStatus.CLEAN
        assert len(store.pending_cleanup_records()) == 0
