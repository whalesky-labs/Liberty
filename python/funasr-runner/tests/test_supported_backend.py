from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest


@pytest.mark.parametrize("script", ["runner.py", "runtime_validate.py", "runtime_warmup.py"])
def test_unsupported_local_backend_fails_before_model_loading(
    repository_root: Path, tmp_path: Path, script: str,
):
    arguments = []
    if script == "runner.py":
        media = tmp_path / "synthetic.wav"
        media.write_bytes(b"synthetic input")
        arguments = [
            "--job-dir", str(tmp_path / "job"), "--input", str(media),
            "--lang", "zh", "--speaker", "false",
        ]
    elif script == "runtime_warmup.py":
        arguments = ["--models-root", str(tmp_path / "models"), "--validate-only"]
    result = subprocess.run(
        [sys.executable, str(repository_root / "python" / "funasr-runner" / script), *arguments],
        env={**os.environ, "LIBERTY_ASR_BACKEND": "unsupported-backend"},
        capture_output=True,
        text=True,
        timeout=15,
        check=False,
    )
    assert result.returncode != 0
    assert "Unsupported local ASR backend" in result.stderr
    assert not (tmp_path / "job" / "result.json").exists()
    assert not list(tmp_path.rglob("*.onnx"))
