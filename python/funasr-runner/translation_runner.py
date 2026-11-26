"""Local-only translation over bounded JSON Lines; no network or credentials."""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import sys
from pathlib import Path

PROTOCOL = "liberty-local-translation"
MAX_LINE = 32768


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate_field")
        result[key] = value
    return result


def parse_request(line, sequence):
    if not line or len(line) > MAX_LINE or not line.endswith(b"\n"):
        raise ValueError("invalid_frame")
    value = json.loads(line, object_pairs_hook=unique_object)
    if not isinstance(value, dict) or set(value) != {"protocol", "version", "sequence", "text"}:
        raise ValueError("invalid_fields")
    if value["protocol"] != PROTOCOL or type(value["version"]) is not int or value["version"] != 1:
        raise ValueError("invalid_version")
    if type(value["sequence"]) is not int or value["sequence"] != sequence + 1:
        raise ValueError("invalid_sequence")
    text = value["text"]
    if not isinstance(text, str) or not text.strip() or len(text.encode()) > 8192:
        raise ValueError("invalid_text")
    return text


def serve(stream, tokenizer, translator, emit):
    emit("ready")
    sequence = 0
    while True:
        text = parse_request(stream.readline(MAX_LINE + 1), sequence)
        tokens = tokenizer.encode(text, out_type=str)
        if len(tokens) > 512:
            raise ValueError("input_too_long")
        results = translator.translate_batch([tokens], beam_size=2, length_penalty=0.2,
                                             replace_unknowns=True, max_decoding_length=512)
        words = results[0].hypotheses[0]
        if len(words) >= 512:
            raise ValueError("translation_truncated")
        translated = tokenizer.decode_pieces(words).replace("▁", " ").strip()
        if not translated or len(translated.encode()) > 8192:
            raise ValueError("invalid_translation")
        sequence += 1
        emit("translation", sequence=sequence, text=translated)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--engine", type=Path, required=True)
    parser.add_argument("--model", type=Path, required=True)
    args = parser.parse_args()
    if not args.engine.is_absolute() or not args.model.is_absolute():
        parser.error("verified absolute model and engine directories required")
    output = sys.stdout

    def emit(kind, **values):
        data = json.dumps(dict(protocol=PROTOCOL, version=1, type=kind, **values),
                          ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        if len(data.encode()) > 16384:
            raise ValueError("response_too_large")
        output.write(data + "\n")
        output.flush()

    try:
        os.environ["OMP_WAIT_POLICY"] = "PASSIVE"
        os.environ["KMP_BLOCKTIME"] = "0"
        with contextlib.redirect_stdout(sys.stderr):
            sys.path.insert(0, str(args.engine))
            import ctranslate2
            import sentencepiece

            if ctranslate2.__version__ != "4.9.1":
                raise ValueError("engine_version_mismatch")
            tokenizer = sentencepiece.SentencePieceProcessor(model_file=str(args.model / "sentencepiece.model"))
            translator = ctranslate2.Translator(str(args.model / "model"), device="cpu",
                                               compute_type="int8", inter_threads=1, intra_threads=1)
            translator.translate_batch([tokenizer.encode("你好。", out_type=str)], beam_size=2,
                                       max_decoding_length=16)
            serve(sys.stdin.buffer, tokenizer, translator, emit)
    except (BrokenPipeError, KeyboardInterrupt):
        return 1
    except Exception:
        emit("error", code="local_translation_failed")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
