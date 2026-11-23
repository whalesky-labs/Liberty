#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path


def parse_args():
    parser = argparse.ArgumentParser(description="Warm up Liberty managed runtime models")
    parser.add_argument("--models-root", required=True)
    parser.add_argument("--validate-only", action="store_true")
    return parser.parse_args()


def log(message: str):
    sys.stdout.write(f"{message}\n")
    sys.stdout.flush()


def resolve_model_profile() -> str:
    profile = str(os.getenv("FUNASR_PROFILE", "") or "").strip().lower()
    if profile in {"sensevoice", "sensevoice-small"}:
        return "sensevoice"
    model_name = str(os.getenv("FUNASR_MODEL", "") or "").strip().lower()
    if "sensevoice" in model_name:
        return "sensevoice"
    return "paraformer"


def main():
    args = parse_args()
    models_root = Path(args.models_root)
    models_root.mkdir(parents=True, exist_ok=True)

    backend = os.getenv("LIBERTY_ASR_BACKEND", "funasr").strip().lower() or "funasr"
    if backend != "funasr":
        raise RuntimeError("不支持的本地 ASR 后端。Unsupported local ASR backend.")

    os.environ.setdefault("MODELSCOPE_CACHE", str(models_root / "modelscope"))
    os.environ.setdefault("HF_HOME", str(models_root / "huggingface"))
    os.environ.setdefault("TORCH_HOME", str(models_root / "torch"))
    if args.validate_only:
        os.environ["MODELSCOPE_OFFLINE"] = "1"
        os.environ["HF_HUB_OFFLINE"] = "1"
        os.environ["TRANSFORMERS_OFFLINE"] = "1"

    log("Validating FunASR models offline..." if args.validate_only else "Importing FunASR runtime...")
    from funasr import AutoModel

    profile = resolve_model_profile()
    default_model = "iic/SenseVoiceSmall" if profile == "sensevoice" else "paraformer-zh"
    common_kwargs = {
        "model": os.getenv("FUNASR_MODEL", default_model),
        "vad_model": os.getenv("FUNASR_VAD_MODEL", "fsmn-vad"),
        "device": "cpu",
        "disable_update": True,
    }
    if profile != "sensevoice":
        common_kwargs["punc_model"] = os.getenv("FUNASR_PUNC_MODEL", "ct-punc")

    log(
        f"Validating cached FunASR models for profile: {profile}"
        if args.validate_only
        else f"Downloading default FunASR models for profile: {profile}"
    )
    AutoModel(**common_kwargs)

    log("Validating cached speaker model..." if args.validate_only else "Downloading default speaker model...")
    AutoModel(
        **common_kwargs,
        spk_model=os.getenv("FUNASR_SPK_MODEL", "cam++"),
    )

    log("Managed model validation completed." if args.validate_only else "Managed runtime warmup completed.")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        sys.stderr.write(f"{error}\n")
        sys.exit(1)
