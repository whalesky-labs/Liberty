"""Bounded, session-owned streaming ASR. stdin/stdout are versioned JSON Lines."""

from __future__ import annotations

import argparse
import base64
import contextlib
import json
import math
import os
import sys
import time
from pathlib import Path

PROTOCOL = "liberty-live-asr"
VERSION = 1
FRAME_BYTES = 3200
MAX_LINE = 8192
MAX_DURATION_MS = 24 * 60 * 60 * 1000


class ProtocolError(ValueError):
    pass


def object_pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ProtocolError("duplicate_field")
        result[key] = value
    return result


class Protocol:
    def __init__(self, output):
        self.output = output
        self.sequence = 0
        self.input_sequence = 0
        self.end_ms = 0

    def emit(self, kind, **values):
        self.sequence += 1
        value = dict(protocol=PROTOCOL, version=VERSION, sequence=self.sequence, type=kind, **values)
        line = json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        if len(line.encode()) > MAX_LINE:
            raise ProtocolError("event_too_large")
        self.output.write(line + "\n")
        self.output.flush()

    def parse(self, line):
        if len(line) > MAX_LINE or not line.endswith(b"\n"):
            raise ProtocolError("invalid_frame")
        value = json.loads(line, object_pairs_hook=object_pairs)
        if not isinstance(value, dict):
            raise ProtocolError("invalid_frame")
        if value.get("protocol") != PROTOCOL or type(value.get("version")) is not int or value.get("version") != VERSION:
            raise ProtocolError("invalid_version")
        sequence = value.get("sequence")
        if type(sequence) is not int or sequence != self.input_sequence + 1:
            raise ProtocolError("invalid_sequence")
        kind = value.get("type")
        if not isinstance(kind, str) or kind not in {"audio", "boundary", "stop"}:
            raise ProtocolError("invalid_type")
        expected = {"protocol", "version", "sequence", "type", "offsetMs"}
        if kind == "audio":
            expected.add("pcm")
        if set(value) != expected:
            raise ProtocolError("invalid_fields")
        offset = value.get("offsetMs")
        if type(offset) is not int or not self.end_ms <= offset <= MAX_DURATION_MS:
            raise ProtocolError("invalid_timeline")
        pcm = b""
        if kind == "audio":
            encoded = value.get("pcm")
            if not isinstance(encoded, str) or len(encoded) > 4268:
                raise ProtocolError("invalid_audio")
            pcm = base64.b64decode(encoded, validate=True)
            if not pcm or len(pcm) > FRAME_BYTES or len(pcm) % 32:
                raise ProtocolError("invalid_audio")
        self.input_sequence = sequence
        self.end_ms = offset + len(pcm) // 32
        if self.end_ms > MAX_DURATION_MS:
            raise ProtocolError("invalid_timeline")
        return kind, offset, pcm


class Decoder:
    """Model caches and audio history are bounded and reset at phrase boundaries."""

    def __init__(self, asr, vad, emit):
        import numpy as np

        self.np, self.asr, self.vad, self.emit = np, asr, vad, emit
        self.segment = 0
        self.revision = 0
        self.end_ms = 0
        self.reset()

    def reset(self):
        self.asr_cache, self.vad_cache = {}, {}
        self.pending = bytearray()
        self.preroll = bytearray()
        self.vad_pending = bytearray()
        self.vad_base = self.end_ms
        self.start_ms = self.end_ms
        self.active = False
        self.text = ""

    def samples(self, pcm):
        return self.np.frombuffer(pcm, dtype="<i2").astype(self.np.float32) / 32768.0

    def infer(self, final=False):
        if not self.pending and not self.asr_cache:
            return
        result = self.asr.generate(
            input=self.samples(bytes(self.pending)), cache=self.asr_cache,
            is_final=final, chunk_size=[0, 10, 5],
            encoder_chunk_look_back=4, decoder_chunk_look_back=1,
            disable_pbar=True, disable_log=True,
        )
        self.pending.clear()
        delta = "".join(str(item.get("text", "")) for item in result)
        # Paraformer returns incremental pieces, not a replacement transcript.
        if delta:
            if self.text and self.text[-1].isascii() and self.text[-1].isalnum() and delta[0].isascii() and delta[0].isalnum():
                self.text += " "
            self.text += delta
            if len(self.text.encode()) > 6000:
                raise ProtocolError("transcript_too_large")
            self.revision += 1
            if not final:
                self.emit("partial", segmentId=self.segment, revision=self.revision,
                          startMs=self.start_ms, endMs=self.end_ms, text=self.text)

    def finish(self, end_ms=None):
        if self.active:
            self.infer(final=True)
            if self.text.strip():
                self.revision += 1
                self.emit("final", segmentId=self.segment, revision=self.revision,
                          startMs=self.start_ms, endMs=max(self.start_ms, end_ms or self.end_ms),
                          text=self.text.strip())
        self.reset()

    def feed(self, pcm, offset):
        if offset != self.end_ms:
            self.finish()
            self.end_ms = offset
            self.reset()
        self.end_ms = offset + len(pcm) // 32
        if self.active:
            self.pending.extend(pcm)
        else:
            self.preroll.extend(pcm)
            del self.preroll[:-19200]  # At most 600 ms before the VAD start.
        self.vad_pending.extend(pcm)
        ending = None
        if len(self.vad_pending) >= 6400:
            result = self.vad.generate(
                input=self.samples(bytes(self.vad_pending)), cache=self.vad_cache,
                is_final=False, chunk_size=200, max_end_silence_time=600,
                disable_pbar=True, disable_log=True,
            )
            self.vad_pending.clear()
            for item in result:
                for start, end in item.get("value", []):
                    if start >= 0 and end != 0 and not self.active:
                        self.active = True
                        self.segment += 1
                        self.revision = 0
                        self.start_ms = max(self.end_ms - len(self.preroll) // 32, self.vad_base + int(start))
                        self.pending.extend(self.preroll)
                        self.preroll.clear()
                    if end > 0 and self.active:
                        ending = min(self.end_ms, self.vad_base + int(end))
        if self.active and len(self.pending) >= 19200:
            self.infer()
        if ending is not None or (self.active and self.end_ms - self.start_ms >= 12000):
            self.finish(ending)
        elif not self.active and self.end_ms - self.vad_base >= 15000:
            self.reset()


def serve(stream, protocol, decoder):
    protocol.emit("ready")
    while True:
        line = stream.readline(MAX_LINE + 1)
        if not line:
            raise ProtocolError("unexpected_eof")
        kind, offset, pcm = protocol.parse(line)
        started = time.monotonic()
        if kind == "audio":
            decoder.feed(pcm, offset)
            values = decoder.samples(pcm)
            level = min(1.0, math.sqrt(float((values * values).mean())))
            protocol.emit("meter", endMs=protocol.end_ms, level=level,
                          processingMs=round((time.monotonic() - started) * 1000))
        else:
            decoder.finish()
            if kind == "stop":
                protocol.emit("stopped", endMs=protocol.end_ms)
                return
            protocol.emit("boundary", endMs=protocol.end_ms)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", type=Path, required=True)
    parser.add_argument("--vad", type=Path, required=True)
    parser.add_argument("--threads", type=int, default=2, choices=range(1, 9))
    args = parser.parse_args()
    for model in [args.model, args.vad]:
        if not model.is_absolute() or not model.is_dir():
            parser.error("validated model directory required")
    for key in ["OMP_NUM_THREADS", "MKL_NUM_THREADS", "NUMEXPR_NUM_THREADS"]:
        os.environ[key] = str(args.threads)
    # Audio arrives every 100 ms: inference threads must sleep between chunks,
    # rather than spend the OpenMP block time spinning while awaiting audio.
    os.environ["OMP_WAIT_POLICY"] = "PASSIVE"
    os.environ["KMP_BLOCKTIME"] = "0"
    for key in ["MODELSCOPE_OFFLINE", "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE"]:
        os.environ[key] = "1"
    protocol = Protocol(sys.stdout)
    try:
        with contextlib.redirect_stdout(sys.stderr):
            from funasr import AutoModel

            common = dict(device="cpu", disable_update=True, disable_pbar=True,
                          disable_log=True, ncpu=args.threads, trust_remote_code=False)
            asr = AutoModel(model=str(args.model), **common)
            vad = AutoModel(model=str(args.vad), **common)
            decoder = Decoder(asr, vad, protocol.emit)
            # Prewarm before granting the application permission to start capture.
            asr.generate(input=decoder.samples(bytes(19200)), cache={}, is_final=True,
                         chunk_size=[0, 10, 5], encoder_chunk_look_back=4,
                         decoder_chunk_look_back=1, disable_pbar=True, disable_log=True)
            for offset in range(0, 600, 100):
                decoder.feed(bytes(FRAME_BYTES), offset)
            decoder.finish()
            decoder.end_ms = 0
            decoder.reset()
            serve(sys.stdin.buffer, protocol, decoder)
    except (KeyboardInterrupt, BrokenPipeError):
        return 1
    except Exception:
        # Never print model paths, captured text, or an exception traceback.
        protocol.emit("error", code="live_asr_failed")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
