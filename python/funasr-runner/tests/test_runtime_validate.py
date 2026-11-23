from __future__ import annotations

import builtins
import importlib.util
import os
import runpy
import subprocess
import sys
from collections import namedtuple
from pathlib import Path
from types import SimpleNamespace

import pytest

VersionInfo = namedtuple("VersionInfo", "major minor micro releaselevel serial")


@pytest.fixture
def validator(repository_root):
    filename = repository_root / "python/funasr-runner/runtime_validate.py"
    spec = importlib.util.spec_from_file_location("liberty_runtime_validator", filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def block_dependencies_and_lock_reads(monkeypatch):
    original_import = builtins.__import__

    def guarded_import(name, *args, **kwargs):
        if name.split(".")[0] in {
            "packaging", "numpy", "torch", "torchaudio", "torchvision", "funasr", "sherpa_onnx",
        }:
            pytest.fail(f"Interpreter preflight imported {name}")
        return original_import(name, *args, **kwargs)

    def forbidden_read(*args, **kwargs):
        pytest.fail("Interpreter preflight read the dependency lock")

    monkeypatch.setattr(builtins, "__import__", guarded_import)
    monkeypatch.setattr(Path, "read_text", forbidden_read)


def fake_backend_imports(monkeypatch, events, numpy_version="1.26.4"):
    original_import = builtins.__import__
    modules = {
        "numpy": SimpleNamespace(__version__=numpy_version),
        "torch": SimpleNamespace(
            __version__="2.2.2", _utils=object(),
            zeros=lambda count: SimpleNamespace(numpy=lambda: None),
        ),
        "torchaudio": SimpleNamespace(__version__="2.2.2"),
        "torchvision": SimpleNamespace(__version__="0.17.2"),
        "transformers": SimpleNamespace(__version__="5.23.0"),
        "transformers.utils": SimpleNamespace(is_torch_available=lambda: False),
        "funasr": SimpleNamespace(AutoModel=SimpleNamespace(__module__="funasr.fixture")),
        "sherpa_onnx": SimpleNamespace(__version__="fixture"),
    }

    def guarded_import(name, *args, **kwargs):
        if name == "packaging.requirements":
            events.append("import:packaging")
        if name in modules:
            events.append(f"import:{name}")
            return modules[name]
        return original_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, "__import__", guarded_import)


def test_managed_validation_requires_installed_pins(tmp_path, repository_root):
    verify = runpy.run_path(
        str(repository_root / "python/funasr-runner/runtime_validate.py")
    )["verify_installed_lock"]
    verify.__globals__["version"] = lambda name: {"example": "1.0.0+cpu"}[name]
    lock = tmp_path / "requirements.txt"
    lock.write_text("# fixture\nexample==1.0.0 \\\n    --hash=sha256:" + "a" * 64)
    verify(lock)
    lock.write_text("example==1.0.1\n")
    with pytest.raises(RuntimeError, match="example"):
        verify(lock)
    lock.write_text("# empty\n")
    with pytest.raises(RuntimeError):
        verify(lock)


@pytest.mark.parametrize("actual", [
    (4, 12, 11, "final", 0), (3, 11, 11, "final", 0), (3, 12, 10, "final", 0),
])
def test_cli_rejects_each_version_mismatch_before_lock_or_imports(validator, monkeypatch, actual):
    monkeypatch.setattr(validator, "sys", SimpleNamespace(
        implementation=SimpleNamespace(name="cpython"), version_info=VersionInfo(*actual),
    ))
    monkeypatch.setattr(sys, "argv", [
        "runtime_validate.py", "--python-version", "3.12.11", "--lock", "must-not-read.txt",
    ])
    block_dependencies_and_lock_reads(monkeypatch)
    with pytest.raises(RuntimeError, match="CPython"):
        validator.main()


@pytest.mark.parametrize("implementation,release,serial", [
    ("pypy", "final", 0), ("graalpy", "final", 0),
    ("cpython", "alpha", 1), ("cpython", "beta", 2), ("cpython", "candidate", 1),
    ("cpython", "final", 1),
])
def test_cli_requires_cpython_final_release_before_lock_or_imports(
    validator, monkeypatch, implementation, release, serial,
):
    monkeypatch.setattr(validator, "sys", SimpleNamespace(
        implementation=SimpleNamespace(name=implementation),
        version_info=VersionInfo(3, 12, 11, release, serial),
    ))
    monkeypatch.setattr(sys, "argv", [
        "runtime_validate.py", "--python-version", "3.12.11", "--lock", "must-not-read.txt",
    ])
    block_dependencies_and_lock_reads(monkeypatch)
    with pytest.raises(RuntimeError, match="CPython"):
        validator.main()


@pytest.mark.parametrize("value", [
    "", "3.12", "3.12.11.0", "v3.12.11", "3.12.11rc1", "3.12.11+local",
    " 3.12.11", "3.12.11 ", "3.12.11\n", "+3.12.11", "3.012.11", "3.12.-1", "٣.12.11",
])
def test_cli_rejects_noncanonical_python_version_before_lock_or_imports(
    validator, monkeypatch, value,
):
    monkeypatch.setattr(sys, "argv", [
        "runtime_validate.py", "--python-version", value, "--lock", "must-not-read.txt",
    ])
    block_dependencies_and_lock_reads(monkeypatch)
    with pytest.raises(SystemExit) as error:
        validator.main()
    assert error.value.code == 2


@pytest.mark.parametrize("arguments", [
    ["--interpreter-only"],
    ["--interpreter-only", "--python-version", "3.12.11", "--lock", "must-not-read.txt"],
])
def test_cli_interpreter_only_flag_contract(validator, monkeypatch, arguments):
    monkeypatch.setattr(sys, "argv", ["runtime_validate.py", *arguments])
    block_dependencies_and_lock_reads(monkeypatch)
    with pytest.raises(SystemExit) as error:
        validator.main()
    assert error.value.code == 2


def test_interpreter_only_uses_stdlib_without_site_packages(repository_root):
    expected = ".".join(map(str, sys.version_info[:3]))
    result = subprocess.run([
        sys.executable, "-I", "-S", str(repository_root / "python/funasr-runner/runtime_validate.py"),
        "--python-version", expected, "--interpreter-only",
    ], capture_output=True, text=True, check=False, env={
        **os.environ, "LIBERTY_ASR_BACKEND": "funasr",
    })
    assert result.returncode == 0, result.stderr
    assert result.stderr == ""
    assert result.stdout == f"CPython {expected} interpreter validation passed.\n"


def test_real_cli_mismatch_fails_before_packaging_or_nonexistent_lock(repository_root, tmp_path):
    expected = f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro + 1}"
    lock = tmp_path / "absent-lock.txt"
    result = subprocess.run([
        sys.executable, "-I", "-S", str(repository_root / "python/funasr-runner/runtime_validate.py"),
        "--python-version", expected, "--lock", str(lock),
    ], capture_output=True, text=True, check=False)
    assert result.returncode == 1
    assert "CPython" in result.stderr
    assert "packaging" not in result.stderr
    assert "absent-lock" not in result.stderr
    assert result.stdout == ""
    assert not lock.exists()


def test_managed_pin_then_lock_then_backend_validation(validator, monkeypatch, tmp_path, capsys):
    events = []
    lock = tmp_path / "requirements.txt"
    lock.write_text("example==1.0.0\n", encoding="utf-8")
    original_read = Path.read_text
    original_verify = getattr(validator, "verify_python_version", None)

    def traced_verify(expected):
        events.append("interpreter")
        assert original_verify is not None
        return original_verify(expected)

    def traced_read(filename, *args, **kwargs):
        if filename == lock:
            events.append("lock-read")
        return original_read(filename, *args, **kwargs)

    monkeypatch.setattr(validator, "verify_python_version", traced_verify, raising=False)
    monkeypatch.setattr(validator, "version", lambda name: "1.0.0")
    monkeypatch.setattr(Path, "read_text", traced_read)
    monkeypatch.setenv("LIBERTY_ASR_BACKEND", "funasr")
    monkeypatch.setattr(sys, "argv", [
        "runtime_validate.py", "--python-version", ".".join(map(str, sys.version_info[:3])),
        "--lock", str(lock),
    ])
    fake_backend_imports(monkeypatch, events)
    validator.main()
    assert events[0] == "interpreter"
    assert events.index("interpreter") < events.index("import:packaging")
    assert events.index("import:packaging") < events.index("lock-read")
    assert events.index("lock-read") < events.index("import:numpy")
    output = capsys.readouterr().out
    assert "torch runtime validation passed." in output
    assert "transformers=5.23.0" in output
    assert "transformers_torch_backend=disabled" in output


@pytest.mark.parametrize("backend,numpy_version,passes", [
    ("sherpa-onnx", "2.0.0", True), ("funasr", "1.26.4", True), ("funasr", "2.0.0", False),
])
def test_unpinned_system_validation_keeps_existing_backend_rules(
    validator, monkeypatch, backend, numpy_version, passes,
):
    def forbidden_pin(expected):
        pytest.fail("Unpinned system validation must not impose an interpreter pin")

    monkeypatch.setattr(validator, "verify_python_version", forbidden_pin, raising=False)
    monkeypatch.setattr(validator, "sys", SimpleNamespace(
        implementation=SimpleNamespace(name="pypy"),
        version_info=VersionInfo(3, 8, 0, "candidate", 1),
    ))
    monkeypatch.setattr(sys, "argv", ["runtime_validate.py"])
    monkeypatch.setenv("LIBERTY_ASR_BACKEND", backend)
    fake_backend_imports(monkeypatch, [], numpy_version)
    if passes:
        validator.main()
    else:
        with pytest.raises(RuntimeError, match="NumPy"):
            validator.main()
