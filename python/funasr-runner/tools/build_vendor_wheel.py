"""Build the pinned local2 candidate from an explicit local upstream wheel, without installation."""

from __future__ import annotations

import argparse
import base64
import csv
import difflib
import hashlib
import io
import json
import os
import re
import stat
import struct
import sys
import zipfile
import zlib
from email import policy
from email.errors import MessageError
from email.parser import BytesParser
from pathlib import Path

RECIPE_PATH = Path(__file__).resolve().parents[1] / "vendor" / "funasr-1.3.9.json"


class VendorWheelError(ValueError):
    pass


def sha256(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def json_bytes(value: object) -> bytes:
    return (json.dumps(value, indent=2, sort_keys=True, ensure_ascii=True) + "\n").encode()


def check(condition: bool, message: str) -> None:
    if not condition:
        raise VendorWheelError(message)


def safe_name(name: str) -> None:
    check(bool(name) and name.isascii() and "\\" not in name, "Unsafe ZIP/RECORD path")
    for part in name.split("/"):
        check(
            bool(re.fullmatch(r"[A-Za-z0-9_.+-]+", part))
            and part not in {".", ".."}
            and not part.endswith(".")
            and not re.fullmatch(r"(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])", part.split(".")[0]),
            "Unsafe ZIP/RECORD path",
        )


def python_manifest(members: dict[str, bytes]) -> dict[str, str]:
    return {name: sha256(content) for name, content in sorted(members.items()) if name.endswith(".py")}


def manifest_digest(manifest: dict[str, str]) -> str:
    return sha256("".join(f"{digest}  {name}\n" for name, digest in manifest.items()).encode())


def record_bytes(members: dict[str, bytes], record_name: str) -> bytes:
    stream = io.StringIO(newline="")
    writer = csv.writer(stream, lineterminator="\r\n")
    for name, content in sorted(members.items()):
        if name != record_name:
            digest = base64.urlsafe_b64encode(hashlib.sha256(content).digest()).rstrip(b"=")
            writer.writerow([name, "sha256=" + digest.decode(), str(len(content))])
    writer.writerow([record_name, "", ""])
    return stream.getvalue().encode()


def validate_record(members: dict[str, bytes], record_name: str) -> None:
    seen = set()
    rows = csv.reader(io.StringIO(members[record_name].decode("utf-8"), newline=""), strict=True)
    for row in rows:
        check(len(row) == 3, "Invalid RECORD column count")
        name, digest, size = row
        safe_name(name)
        check(name not in seen and name in members, "Duplicate or unknown RECORD member")
        seen.add(name)
        if name == record_name:
            check(digest == size == "", "RECORD must not hash itself")
        else:
            expected = base64.urlsafe_b64encode(hashlib.sha256(members[name]).digest()).rstrip(b"=")
            check(digest == "sha256=" + expected.decode(), "RECORD SHA-256 mismatch")
            check(size == str(len(members[name])), "RECORD size mismatch")
    check(seen == members.keys(), "RECORD does not cover every member")


def headers(content: bytes, required: dict[str, str]):
    check(b"\r" not in content and b"\x00" not in content, "Invalid metadata encoding")
    message = BytesParser(policy=policy.strict).parsebytes(content)
    for field, value in required.items():
        check(message.get_all(field, []) == [value], f"Unexpected {field} metadata")
    return message


def read_wheel(content: bytes, recipe: dict, *, local: bool = False) -> dict[str, bytes]:
    limits = recipe["limits"]
    check(len(content) <= limits["archiveBytes"], "Archive exceeds byte limit")
    check(len(content) >= 22 and content.startswith(b"PK\x03\x04"), "Invalid ZIP envelope")
    ending = struct.unpack("<4s4H2LH", content[-22:])
    check(
        ending[0] == b"PK\x05\x06" and ending[1] == ending[2] == ending[7] == 0
        and ending[3] == ending[4] and ending[5] + ending[6] == len(content) - 22,
        "Unsupported ZIP envelope, trailing data or multi-disk archive",
    )
    version = recipe["output" if local else "upstream"]["version"]
    prefix = f"funasr-{version}.dist-info/"
    members = {}
    folded = set()
    with zipfile.ZipFile(io.BytesIO(content)) as archive:
        infos = archive.infolist()
        check(ending[3] == len(infos), "ZIP central directory entry count mismatch")
        check(len(infos) <= limits["entries"], "ZIP entry limit exceeded")
        check(len(infos) == recipe["inventory"]["files"], "Unexpected wheel inventory")
        total = 0
        for entry in infos:
            name = entry.filename
            safe_name(name)
            check(name == entry.orig_filename, "Truncated ZIP path")
            check(name.casefold() not in folded, "Duplicate or case-colliding ZIP path")
            folded.add(name.casefold())
            check(name.startswith(("funasr/", prefix)), "Unexpected wheel root or dist-info")
            check(
                entry.create_system == 3 and stat.S_ISREG(entry.external_attr >> 16)
                and not entry.external_attr & 0x10 and not entry.extra and not entry.comment,
                "ZIP links, directories, special files or extra fields are forbidden",
            )
            check(
                not entry.flag_bits & ~0x800
                and entry.compress_type in {zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED},
                "Unsupported ZIP flags or compression",
            )
            check(entry.file_size <= limits["entryBytes"], "ZIP member byte limit exceeded")
            check(
                entry.file_size <= max(entry.compress_size, 1) * limits["compressionRatio"],
                "ZIP compression ratio limit exceeded",
            )
            total += entry.file_size
            check(total <= limits["totalBytes"], "ZIP total byte limit exceeded")
        for entry in infos:
            with archive.open(entry) as stream:
                data = stream.read(limits["entryBytes"] + 1)
            check(len(data) == entry.file_size, "ZIP member size mismatch")
            members[entry.filename] = data
    for name in members:
        parents = name.split("/")[:-1]
        check(
            all("/".join(parents[:index]) not in members for index in range(1, len(parents) + 1)),
            "ZIP file/directory collision",
        )
    required = [prefix + name for name in ["METADATA", "WHEEL", "RECORD"]]
    check(all(name in members for name in required), "Missing METADATA, WHEEL or RECORD")
    inventory = recipe["inventory"]
    metadata = members[prefix + "METADATA"]
    check(
        sha256(metadata) == inventory["patchedMetadataSha256" if local else "metadataSha256"],
        "Unexpected METADATA digest",
    )
    message = headers(metadata, {
        "Metadata-Version": "2.4", "Name": "funasr", "Version": version,
        "License-File": "LICENSE", "Requires-Python": ">=3.7.0",
    })
    requirements = message.get_all("Requires-Dist", [])
    if local:
        check(
            requirements.count('oss2; extra == "oss"') == 1
            and requirements.count("six>=1.16.0") == 1 and "oss2" not in requirements
            and message.get_all("Provides-Extra", []).count("oss") == 1,
            "Unexpected patched dependency metadata",
        )
    else:
        check(requirements.count("oss2") == 1, "Expected exactly one unconditional oss2 dependency")
        check(sha256(members[prefix + "RECORD"]) == inventory["recordSha256"], "Unexpected RECORD digest")
    wheel = members[prefix + "WHEEL"]
    check(sha256(wheel) == inventory["wheelMetadataSha256"], "Unexpected WHEEL digest")
    wheel_message = headers(wheel, {
        "Wheel-Version": "1.0", "Root-Is-Purelib": "true", "Tag": "py3-none-any",
    })
    check(wheel_message.get_payload() == "" and not wheel_message.get_all("Build"), "Invalid WHEEL body or build tag")
    validate_record(members, prefix + "RECORD")
    sources = python_manifest(members)
    check(len(sources) == inventory["pythonFiles"], "Python source inventory count mismatch")
    check(manifest_digest(sources) == inventory["pythonManifestSha256"], "Python source manifest mismatch")
    license_name = prefix + inventory["license"]["path"]
    check(license_name in members, "Missing upstream license")
    check(sha256(members[license_name]) == inventory["license"]["sha256"], "License digest mismatch")
    check(members.get("funasr/version.txt") == (version + "\n").encode(), "Unexpected version.txt")
    return members


def patch_members(original: dict[str, bytes], recipe: dict) -> tuple[dict[str, bytes], bytes]:
    upstream_version = recipe["upstream"]["version"]
    local_version = recipe["output"]["version"]
    before_prefix = f"funasr-{upstream_version}.dist-info/"
    after_prefix = f"funasr-{local_version}.dist-info/"
    before = original[before_prefix + "METADATA"].decode("utf-8")
    header, separator, body = before.partition("\n\n")
    check(bool(separator), "METADATA header terminator missing")
    header += "\n"
    for old, new in [
        (f"Version: {upstream_version}\n", f"Version: {local_version}\n"),
        (recipe["patch"]["dependencyBefore"], recipe["patch"]["dependencyAfter"]),
    ]:
        check(header.count(old) == 1, "Patch precondition mismatch")
        header = header.replace(old, new, 1)
    after = header + "\n" + body
    diff = "".join(difflib.unified_diff(
        before.splitlines(keepends=True), after.splitlines(keepends=True),
        fromfile="upstream/METADATA", tofile="local/METADATA",
    )).encode()
    check(sha256(diff) == recipe["patch"]["metadataDiffSha256"], "Metadata patch digest mismatch")
    patched = {
        after_prefix + name[len(before_prefix):] if name.startswith(before_prefix) else name: content
        for name, content in original.items() if name != before_prefix + "RECORD"
    }
    patched[after_prefix + "METADATA"] = after.encode()
    patched["funasr/version.txt"] = (local_version + "\n").encode()
    patched[after_prefix + "RECORD"] = record_bytes(patched, after_prefix + "RECORD")
    for name, content in original.items():
        if name in {before_prefix + "METADATA", before_prefix + "RECORD", "funasr/version.txt"}:
            continue
        target = after_prefix + name[len(before_prefix):] if name.startswith(before_prefix) else name
        check(patched[target] == content, "Unexpected payload modification")
    check(python_manifest(original) == python_manifest(patched), "Python source modification")
    return patched, diff


def pack_wheel(members: dict[str, bytes], recipe: dict) -> bytes:
    settings = recipe["archive"]
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, "w", compression=zipfile.ZIP_DEFLATED, allowZip64=False) as archive:
        for name, content in sorted(members.items()):
            entry = zipfile.ZipInfo(name, date_time=tuple(settings["timestamp"]))
            entry.create_system = settings["createSystem"]
            entry.external_attr = settings["mode"] << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(entry, content, compresslevel=settings["compressionLevel"])
    return stream.getvalue()


def read_source(source: Path, recipe: dict) -> bytes:
    check(stat.S_ISREG(source.lstat().st_mode), "Upstream wheel must be a regular file, not a symlink")
    flags = (
        os.O_RDONLY | getattr(os, "O_BINARY", 0)
        | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0)
    )
    with os.fdopen(os.open(source, flags), "rb") as stream:
        info = os.fstat(stream.fileno())
        check(stat.S_ISREG(info.st_mode), "Upstream wheel must be a regular file")
        check(info.st_size <= recipe["limits"]["archiveBytes"], "Archive exceeds byte limit")
        content = stream.read(recipe["limits"]["archiveBytes"] + 1)
    check(len(content) == recipe["upstream"]["bytes"], "Upstream wheel size mismatch")
    check(sha256(content) == recipe["upstream"]["sha256"], "Upstream wheel SHA-256 mismatch")
    return content


def source_sbom(recipe: dict, wheel: bytes, recipe_digest: str, patch_digest: str) -> dict:
    upstream = recipe["upstream"]
    return {
        "bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1,
        "metadata": {"component": {
            "type": "file", "bom-ref": "liberty:vendor-wheel", "name": recipe["output"]["filename"],
            "version": recipe["output"]["version"],
            "hashes": [{"alg": "SHA-256", "content": sha256(wheel)}],
            "properties": [
                {"name": "liberty:recipe:sha256", "value": recipe_digest},
                {"name": "liberty:metadata-patch:sha256", "value": patch_digest},
                {"name": "liberty:security-gate-established", "value": "false"},
            ],
        }},
        "components": [{
            "type": "library", "bom-ref": upstream["purl"], "name": upstream["name"],
            "version": upstream["version"], "purl": upstream["purl"],
            "hashes": [{"alg": "SHA-256", "content": upstream["sha256"]}],
            "licenses": [{"license": {"id": recipe["inventory"]["license"]["id"]}}],
            "externalReferences": [{
                "type": "distribution", "url": upstream["url"],
                "hashes": [{"alg": "SHA-256", "content": upstream["sha256"]}],
            }],
        }],
        "dependencies": [{"ref": "liberty:vendor-wheel", "dependsOn": [upstream["purl"]]}],
    }


def publish(output: Path, artifacts: dict[str, bytes]) -> None:
    check(not output.is_symlink(), "Output directory must not be a symlink")
    try:
        output.mkdir(mode=0o700)
    except FileExistsError as error:
        raise VendorWheelError("Output directory already exists; refusing to overwrite") from error
    created = []
    try:
        for name, content in artifacts.items():
            safe_name(name)
            check("/" not in name, "Output artifacts must be direct children")
            with (output / name).open("xb") as stream:
                created.append(output / name)
                stream.write(content)
    except BaseException:
        for filename in created:
            filename.unlink(missing_ok=True)
        try:
            output.rmdir()
        except OSError:
            pass
        raise


def build_vendor_wheel(source: Path, output: Path) -> dict:
    recipe_content = RECIPE_PATH.read_bytes()
    recipe = json.loads(recipe_content)
    check(recipe["schemaVersion"] == 1, "Unsupported recipe schema")
    original = read_wheel(read_source(source, recipe), recipe)
    members, diff = patch_members(original, recipe)
    wheel = pack_wheel(members, recipe)
    check(len(wheel) == recipe["output"]["bytes"], "Output size differs from the fixed recipe")
    check(sha256(wheel) == recipe["output"]["sha256"], "Output SHA-256 differs from the fixed recipe; compressor/build mismatch")
    read_wheel(wheel, recipe, local=True)
    recipe_digest = sha256(recipe_content)
    sbom = json_bytes(source_sbom(recipe, wheel, recipe_digest, sha256(diff)))
    provenance = {
        "schemaVersion": 1,
        "recipe": {"id": recipe["id"], "filename": "recipe.json", "sha256": recipe_digest},
        "builder": {"path": "python/funasr-runner/tools/build_vendor_wheel.py", "sha256": sha256(Path(__file__).read_bytes())},
        "upstream": recipe["upstream"],
        "patch": {"filename": "METADATA.patch", "sha256": sha256(diff), "scope": recipe["patch"]["scope"]},
        "artifact": {**recipe["output"], "sha256": sha256(wheel), "bytes": len(wheel)},
        "preservation": {
            "pythonFiles": python_manifest(original),
            "pythonFileCount": len(python_manifest(original)),
            "pythonManifestSha256": manifest_digest(python_manifest(original)),
            "allOtherPayloadBytesUnchanged": True,
            "bookkeepingExceptions": ["METADATA", "version.txt", "dist-info paths", "RECORD"],
            "license": recipe["inventory"]["license"],
        },
        "sourceSbom": {"filename": "source.cdx.json", "sha256": sha256(sbom)},
        "productionSecurityGateEstablished": False,
        "deliveryBoundary": recipe["deliveryBoundary"],
    }
    publish(output, {
        recipe["output"]["filename"]: wheel, "METADATA.patch": diff,
        "recipe.json": recipe_content, "source.cdx.json": sbom,
        "provenance.json": json_bytes(provenance),
    })
    return provenance


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream-wheel", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True, help="New directory; parent must exist")
    arguments = parser.parse_args(argv)
    try:
        provenance = build_vendor_wheel(arguments.upstream_wheel, arguments.output_dir)
    except (ValueError, KeyError, TypeError, MessageError, zipfile.BadZipFile, zipfile.LargeZipFile, zlib.error, csv.Error, EOFError) as error:
        print(f"Vendor wheel build refused: {error}", file=sys.stderr)
        return 1
    except OSError as error:
        print(f"Vendor wheel filesystem operation failed (errno {error.errno})", file=sys.stderr)
        return 1
    print(json_bytes({"artifact": provenance["artifact"], "productionSecurityGateEstablished": False}).decode(), end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
