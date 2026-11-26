from __future__ import annotations

import base64
import importlib.util
import io
import json
from pathlib import Path

import pytest


@pytest.fixture
def live():
    spec = importlib.util.spec_from_file_location("live_runner_test", Path(__file__).parents[1] / "live_runner.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def message(kind="audio", seq=1, offset=0):
    value = dict(protocol="liberty-live-asr", version=1, sequence=seq, type=kind, offsetMs=offset)
    if kind == "audio":
        value["pcm"] = base64.b64encode(bytes(3200)).decode()
    return value


def line(value):
    return (json.dumps(value) + "\n").encode()


def test_live_protocol_preserves_pause_gap_and_rejects_replay(live):
    protocol = live.Protocol(io.StringIO())
    assert protocol.parse(line(message())) == ("audio", 0, bytes(3200))
    assert protocol.parse(line(message("boundary", 2, 100))) == ("boundary", 100, b"")
    assert protocol.parse(line(message(seq=3, offset=2100)))[1] == 2100
    with pytest.raises(live.ProtocolError):
        protocol.parse(line(message(seq=3, offset=2100)))
    with pytest.raises(live.ProtocolError):
        protocol.parse(line(message(seq=4, offset=2199)))
    assert protocol.parse(line(message("stop", 4, 2200)))[0] == "stop"


@pytest.mark.parametrize("key,value", [("version", True), ("sequence", True), ("sequence", 2), ("offsetMs", -1), ("offsetMs", 0.5), ("pcm", "!"), ("pcm", ""), ("pcm", "A" * 5000), ("extra", "ignored")])
def test_live_protocol_rejects_malformed_or_unbounded_audio(live, key, value):
    data = message()
    data[key] = value
    with pytest.raises((live.ProtocolError, ValueError)):
        live.Protocol(io.StringIO()).parse(line(data))


def test_live_duplicate_keys_and_partial_lines_fail_closed(live):
    with pytest.raises(live.ProtocolError):
        live.Protocol(io.StringIO()).parse(line(message()).replace(b'"version": 1', b'"version": 1,"version": 1'))
    with pytest.raises(live.ProtocolError):
        live.Protocol(io.StringIO()).parse(line(message()).rstrip())


def test_live_stop_flushes_before_stopped_and_eof_is_not_success(live):
    output = io.StringIO()
    protocol = live.Protocol(output)

    class Decoder:
        def finish(self):
            protocol.emit("final", segmentId=1, revision=1, startMs=0, endMs=500, text="synthetic tail")

    live.serve(io.BytesIO(line(message("stop"))), protocol, Decoder())
    assert [json.loads(value)["type"] for value in output.getvalue().splitlines()] == ["ready", "final", "stopped"]
    with pytest.raises(live.ProtocolError):
        live.serve(io.BytesIO(), live.Protocol(io.StringIO()), Decoder())
