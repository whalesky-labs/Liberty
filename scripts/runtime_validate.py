#!/usr/bin/env python3
from __future__ import annotations

import sys


def main():
    import torch
    import torchaudio
    import torchvision

    if not hasattr(torch, "_utils"):
        raise RuntimeError("torch._utils 缺失，当前 PyTorch 安装不完整。")

    print(f"torch={torch.__version__}")
    print(f"torchvision={torchvision.__version__}")
    print(f"torchaudio={torchaudio.__version__}")
    print("torch runtime validation passed.")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        sys.stderr.write(f"{error}\n")
        sys.exit(1)
