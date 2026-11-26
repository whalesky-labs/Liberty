from __future__ import annotations

import importlib.util
import io
import json
from pathlib import Path
from types import SimpleNamespace

import pytest


@pytest.fixture
def translation():
    spec = importlib.util.spec_from_file_location(
        "translation_runner_test", Path(__file__).parents[1] / "translation_runner.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def frame(**updates):
    value = dict(protocol="liberty-local-translation", version=1, sequence=1, text="会议开始。")
    value.update(updates)
    return (json.dumps(value, ensure_ascii=False) + "\n").encode()


@pytest.mark.parametrize("updates", [
    {"version": True}, {"Version": 1}, {"protocol": "other"}, {"sequence": True},
    {"sequence": 0}, {"sequence": 2}, {"text": " "}, {"text": 1},
    {"text": "中" * 2731}, {"api_key": "synthetic"},
])
def test_rejects_invalid_requests(translation, updates):
    with pytest.raises(ValueError):
        translation.parse_request(frame(**updates), 0)


def test_request_order_duplicates_and_size(translation):
    assert translation.parse_request(frame(), 0) == "会议开始。"
    assert translation.parse_request(frame(sequence=2), 1) == "会议开始。"
    for data in [frame(), frame().rstrip(), b"x" * 32769 + b"\n",
                 frame().replace(b'"version": 1', b'"version": 1,"version": 1'), b"[]\n"]:
        with pytest.raises(ValueError):
            translation.parse_request(data, 1)


class Tokenizer:
    def encode(self, text, out_type):
        return text.split()

    def decode_pieces(self, words):
        return "".join(words)


def test_translation_emits_once_and_eof_is_not_success(translation):
    events = []
    translator = SimpleNamespace(translate_batch=lambda *args, **kwargs:
                                 [SimpleNamespace(hypotheses=[["▁Meeting", "▁starts."]])])
    with pytest.raises(ValueError, match="invalid_frame"):
        translation.serve(io.BytesIO(frame() + frame(sequence=2)), Tokenizer(), translator,
                          lambda kind, **values: events.append((kind, values)))
    assert [kind for kind, _ in events] == ["ready", "translation", "translation"]
    assert events[-1][1] == {"sequence": 2, "text": "Meeting starts."}


@pytest.mark.parametrize("text,output,reason", [
    ("token " * 513, ["hello"], "input_too_long"),
    ("hello", ["word"] * 512, "translation_truncated"),
    ("hello", [" "], "invalid_translation"),
    ("hello", ["中" * 2731], "invalid_translation"),
])
def test_refuses_silent_truncation(translation, text, output, reason):
    translator = SimpleNamespace(translate_batch=lambda *args, **kwargs:
                                 [SimpleNamespace(hypotheses=[output])])
    with pytest.raises(ValueError, match=reason):
        translation.serve(io.BytesIO(frame(text=text)), Tokenizer(), translator,
                          lambda *args, **kwargs: None)
