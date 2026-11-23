#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import re
import sys
from importlib.metadata import version
from pathlib import Path


def parse_python_version(value: str) -> str:
    component = r"(?:0|[1-9][0-9]*)"
    if re.fullmatch(rf"{component}\.{component}\.{component}", value) is None:
        raise argparse.ArgumentTypeError("Python version must be a strict X.Y.Z release version.")
    return value


def verify_python_version(expected: str) -> None:
    actual = ".".join(map(str, sys.version_info[:3]))
    if (
        sys.implementation.name != "cpython"
        or sys.version_info.releaselevel != "final"
        or sys.version_info.serial != 0
        or actual != expected
    ):
        raise RuntimeError(
            f"托管 Python 解释器不匹配：需要正式版 CPython {expected}，"
            f"当前为 {sys.implementation.name} {actual} "
            f"({sys.version_info.releaselevel}, {sys.version_info.serial})。"
        )


def verify_installed_lock(lock: Path):
    from packaging.requirements import Requirement

    pins = 0
    for line in lock.read_text(encoding="utf-8").splitlines():
        entry = line.strip().removesuffix("\\").strip()
        if not entry or entry.startswith(("#", "--hash=sha256:")):
            continue
        required = Requirement(entry)
        if version(required.name) not in required.specifier:
            raise RuntimeError(f"托管 Python 依赖版本与平台锁不一致：{required.name}")
        pins += 1
    if not pins:
        raise RuntimeError("托管 Python 依赖锁为空。")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--python-version", type=parse_python_version)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--lock", type=Path)
    mode.add_argument("--interpreter-only", action="store_true")
    args = parser.parse_args()
    if args.interpreter_only and args.python_version is None:
        parser.error("--interpreter-only requires --python-version")
    if args.python_version is not None:
        verify_python_version(args.python_version)
    if args.interpreter_only:
        print(f"CPython {args.python_version} interpreter validation passed.")
        return
    if args.lock is not None:
        verify_installed_lock(args.lock)
    backend = os.getenv("LIBERTY_ASR_BACKEND", "funasr").strip().lower() or "funasr"
    if backend == "sherpa-onnx":
        import numpy
        import sherpa_onnx

        numpy_major = int(numpy.__version__.split(".", 1)[0])
        if numpy_major < 1:
            raise RuntimeError(f"NumPy 版本不可用，当前为 {numpy.__version__}。")

        print(f"numpy={numpy.__version__}")
        print(f"sherpa_onnx={getattr(sherpa_onnx, '__version__', 'unknown')}")
        print("sherpa-onnx runtime validation passed.")
        return

    import numpy
    import torch
    import torchaudio
    import torchvision
    import transformers
    from funasr import AutoModel
    from transformers.utils import is_torch_available

    if not hasattr(torch, "_utils"):
        raise RuntimeError("torch._utils 缺失，当前 PyTorch 安装不完整。")

    numpy_major = int(numpy.__version__.split(".", 1)[0])
    if numpy_major >= 2:
        raise RuntimeError(
            f"NumPy 版本不兼容，当前为 {numpy.__version__}，需要使用 numpy<2。"
        )

    try:
        torch.zeros(1).numpy()
    except Exception as error:
        raise RuntimeError(f"PyTorch 与 NumPy 集成不可用：{error}") from error

    print(f"numpy={numpy.__version__}")
    print(f"torch={torch.__version__}")
    print(f"torchvision={torchvision.__version__}")
    print(f"torchaudio={torchaudio.__version__}")
    print(f"transformers={transformers.__version__}")
    # The pinned FunASR Paraformer/VAD/punctuation/speaker implementations use
    # Torch directly. Transformers 5 requires Torch >=2.6 for its own models;
    # Intel macOS's final official Torch wheel is 2.2.2. Report that boundary
    # explicitly; successful imports do not certify Transformers Torch models.
    print(f"transformers_torch_backend={'enabled' if is_torch_available() else 'disabled'}")
    print(f"funasr={AutoModel.__module__}")
    print("torch runtime validation passed.")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        sys.stderr.write(f"{error}\n")
        sys.exit(1)
