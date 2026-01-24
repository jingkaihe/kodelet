"""Tests for Kodelet client."""

import tempfile
from pathlib import Path
from unittest.mock import patch

import pytest

from kodelet import Kodelet, KodeletConfig
from kodelet.exceptions import BinaryNotFoundError
from kodelet.hooks import Hook, HookType


def test_config_defaults():
    config = KodeletConfig()

    assert config.provider == "anthropic"
    assert config.model == "claude-sonnet-4-5-20250929"
    assert config.max_tokens == 8192
    assert config.stream_deltas is True
    assert config.no_hooks is False


def test_config_custom_values():
    config = KodeletConfig(
        provider="openai",
        model="gpt-4.1",
        max_tokens=4096,
        allowed_tools=["bash", "file_read"],
    )

    assert config.provider == "openai"
    assert config.model == "gpt-4.1"
    assert config.max_tokens == 4096
    assert config.allowed_tools == ["bash", "file_read"]


def test_client_raises_on_missing_binary():
    with patch("shutil.which", return_value=None), pytest.raises(BinaryNotFoundError):
        Kodelet()


def test_client_finds_binary_in_path():
    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        agent = Kodelet()
        assert agent._binary == Path("/usr/bin/kodelet")


def test_client_uses_custom_binary_path():
    with tempfile.NamedTemporaryFile(delete=False) as f:
        binary_path = Path(f.name)

    try:
        config = KodeletConfig(kodelet_path=binary_path)
        agent = Kodelet(config=config)
        assert agent._binary == binary_path
    finally:
        binary_path.unlink()


def test_client_raises_on_invalid_custom_path():
    config = KodeletConfig(kodelet_path=Path("/nonexistent/kodelet"))
    with pytest.raises(BinaryNotFoundError):
        Kodelet(config=config)


def test_build_command_basic():
    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        agent = Kodelet()
        cmd = agent._build_command("test query")

        assert cmd[0] == "/usr/bin/kodelet"
        assert "run" in cmd
        assert "--headless" in cmd
        assert "--stream-deltas" in cmd
        assert "test query" in cmd


def test_build_command_with_config():
    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        config = KodeletConfig(
            provider="openai",
            model="gpt-4.1",
            max_tokens=4096,
            allowed_tools=["bash", "file_read"],
            no_skills=True,
            no_hooks=True,
        )
        agent = Kodelet(config=config)
        cmd = agent._build_command("test")

        assert "--provider" in cmd
        assert "openai" in cmd
        assert "--model" in cmd
        assert "gpt-4.1" in cmd
        assert "--max-tokens" in cmd
        assert "4096" in cmd
        assert "--allowed-tools" in cmd
        assert "bash,file_read" in cmd
        assert "--no-skills" in cmd
        assert "--no-hooks" in cmd


def test_build_command_with_resume():
    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        agent = Kodelet(resume="conv-123")
        cmd = agent._build_command("continue")

        assert "--resume" in cmd
        assert "conv-123" in cmd


def test_build_command_with_follow():
    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        agent = Kodelet(follow=True)
        cmd = agent._build_command("continue")

        assert "--follow" in cmd


def test_client_with_hooks():
    @Hook(HookType.BEFORE_TOOL_CALL)
    def my_hook(payload: dict) -> dict:
        return {"blocked": False}

    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        agent = Kodelet(hooks=[my_hook])

        assert len(agent._hook_manager.hooks) == 1
        assert "my_hook" in agent._hook_manager.hooks


def test_setup_hooks_creates_scripts():
    @Hook(HookType.BEFORE_TOOL_CALL)
    def test_hook(payload: dict) -> dict:
        return {"blocked": False}

    with patch("shutil.which", return_value="/usr/bin/kodelet"):
        agent = Kodelet(hooks=[test_hook])

        with tempfile.TemporaryDirectory() as tmpdir:
            cwd = Path(tmpdir)
            scripts = agent._setup_hooks(cwd)

            assert len(scripts) == 1
            assert scripts[0].exists()
            assert "_pysdk_test_hook" in scripts[0].name

            # Cleanup
            agent._cleanup_hooks(scripts)
            assert not scripts[0].exists()
