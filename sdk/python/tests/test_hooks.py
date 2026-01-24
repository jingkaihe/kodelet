"""Tests for hooks module."""

import tempfile
from pathlib import Path

from kodelet.hooks import Hook, HookDefinition, HookManager, HookType


def test_hook_decorator():
    @Hook(HookType.BEFORE_TOOL_CALL)
    def my_hook(payload: dict) -> dict:
        return {"blocked": False}

    assert hasattr(my_hook, "_hook_definition")
    defn = my_hook._hook_definition
    assert isinstance(defn, HookDefinition)
    assert defn.name == "my_hook"
    assert defn.hook_type == HookType.BEFORE_TOOL_CALL
    assert defn.func is my_hook


def test_hook_decorator_with_custom_name():
    @Hook(HookType.AFTER_TOOL_CALL, name="custom_name")
    def my_hook(payload: dict) -> dict:
        return {}

    defn = my_hook._hook_definition
    assert defn.name == "custom_name"


def test_hook_manager_collects_hooks():
    @Hook(HookType.BEFORE_TOOL_CALL)
    def hook1(payload: dict) -> dict:
        return {"blocked": False}

    @Hook(HookType.AFTER_TOOL_CALL)
    def hook2(payload: dict) -> dict:
        return {}

    manager = HookManager([hook1, hook2])

    assert len(manager.hooks) == 2
    assert "hook1" in manager.hooks
    assert "hook2" in manager.hooks


def test_hook_manager_ignores_non_hooks():
    def regular_function():
        pass

    @Hook(HookType.BEFORE_TOOL_CALL)
    def actual_hook(payload: dict) -> dict:
        return {}

    manager = HookManager([regular_function, actual_hook])

    assert len(manager.hooks) == 1
    assert "actual_hook" in manager.hooks


def test_generate_hook_script():
    @Hook(HookType.BEFORE_TOOL_CALL)
    def security_check(payload: dict) -> dict:
        if payload.get("tool_name") == "bash":
            return {"blocked": True, "reason": "No bash"}
        return {"blocked": False}

    manager = HookManager([security_check])
    script = manager._generate_hook_script(manager.hooks["security_check"])

    # Check script structure
    assert "#!/usr/bin/env python3" in script
    assert "before_tool_call" in script
    assert "def security_check(payload: dict) -> dict:" in script
    assert 'if payload.get("tool_name") == "bash":' in script
    assert "json.load(sys.stdin)" in script


def test_generate_hook_scripts_creates_files():
    @Hook(HookType.BEFORE_TOOL_CALL)
    def test_hook(payload: dict) -> dict:
        return {"blocked": False}

    manager = HookManager([test_hook])

    with tempfile.TemporaryDirectory() as tmpdir:
        hooks_dir = Path(tmpdir)
        manager.generate_hook_scripts(hooks_dir)

        script_path = hooks_dir / "test_hook"
        assert script_path.exists()
        assert script_path.stat().st_mode & 0o755  # Check executable


def test_hook_types():
    assert HookType.BEFORE_TOOL_CALL.value == "before_tool_call"
    assert HookType.AFTER_TOOL_CALL.value == "after_tool_call"
    assert HookType.USER_MESSAGE_SEND.value == "user_message_send"
    assert HookType.AGENT_STOP.value == "agent_stop"
    assert HookType.TURN_END.value == "turn_end"
