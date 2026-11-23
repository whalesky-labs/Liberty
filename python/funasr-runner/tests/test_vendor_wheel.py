from __future__ import annotations

import base64
import csv
import difflib
import hashlib
import importlib.util
import io
import json
import os
import stat
import struct
import warnings
import zipfile
from pathlib import Path

import pytest


def digest(content):
    return hashlib.sha256(content).hexdigest()


def encode_record(members, prefix):
    stream = io.StringIO(newline="")
    writer = csv.writer(stream)
    for name, content in sorted(members.items()):
        value = base64.urlsafe_b64encode(hashlib.sha256(content).digest()).rstrip(b"=")
        writer.writerow([name, "sha256=" + value.decode(), len(content)])
    writer.writerow([prefix + "RECORD", "", ""])
    return stream.getvalue().encode()


def encode_zip(members, *, modes=None, extras=None, method=zipfile.ZIP_DEFLATED):
    stream = io.BytesIO()
    pairs = members.items() if isinstance(members, dict) else members
    with warnings.catch_warnings(), zipfile.ZipFile(stream, "w") as archive:
        warnings.simplefilter("ignore", UserWarning)
        for name, content in sorted(pairs):
            entry = zipfile.ZipInfo(name, date_time=(2026, 11, 19, 0, 0, 0))
            entry.create_system = 3
            entry.external_attr = (modes or {}).get(name, stat.S_IFREG | 0o644) << 16
            entry.extra = (extras or {}).get(name, b"")
            entry.compress_type = method
            archive.writestr(entry, content, compresslevel=6)
    return stream.getvalue()


@pytest.fixture
def vendor_module(repository_root):
    filename = repository_root / "python/funasr-runner/tools/build_vendor_wheel.py"
    spec = importlib.util.spec_from_file_location("liberty_vendor_wheel", filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def wheel_fixture(vendor_module, tmp_path, monkeypatch):
    recipe = json.loads(vendor_module.RECIPE_PATH.read_bytes())
    prefix = "funasr-1.3.9.dist-info/"
    metadata = (
        "Metadata-Version: 2.4\nName: funasr\nVersion: 1.3.9\n"
        "License-File: LICENSE\nRequires-Python: >=3.7.0\n"
        "Requires-Dist: oss2\n\nDescription kept byte-for-byte.\n"
    ).encode()
    members = {
        "funasr/__init__.py": b'raise RuntimeError("wheel payload must never execute")\n',
        "funasr/model.py": b"MODEL_BEHAVIOR = 'unchanged'\n",
        "funasr/version.txt": b"1.3.9\n",
        prefix + "METADATA": metadata,
        prefix + "WHEEL": (
            "Wheel-Version: 1.0\nGenerator: fixture\nRoot-Is-Purelib: true\n"
            "Tag: py3-none-any\n\n"
        ).encode(),
        prefix + "licenses/LICENSE": b"MIT fixture license must remain unchanged.\n",
    }
    members[prefix + "RECORD"] = encode_record(members, prefix)
    upstream = encode_zip(members)
    after = metadata.decode().replace("Version: 1.3.9\n", "Version: 1.3.9+liberty.local2\n")
    after = after.replace("Requires-Dist: oss2\n", recipe["patch"]["dependencyAfter"]).encode()
    patch = "".join(difflib.unified_diff(
        metadata.decode().splitlines(keepends=True), after.decode().splitlines(keepends=True),
        fromfile="upstream/METADATA", tofile="local/METADATA",
    )).encode()
    expected = {
        name.replace(prefix, "funasr-1.3.9+liberty.local2.dist-info/"): content
        for name, content in members.items() if not name.endswith("/RECORD")
    }
    expected["funasr/version.txt"] = b"1.3.9+liberty.local2\n"
    expected["funasr-1.3.9+liberty.local2.dist-info/METADATA"] = after
    expected["funasr-1.3.9+liberty.local2.dist-info/RECORD"] = encode_record(
        expected, "funasr-1.3.9+liberty.local2.dist-info/",
    )
    wheel = encode_zip(expected)
    recipe["upstream"].update(bytes=len(upstream), sha256=digest(upstream))
    recipe["output"].update(bytes=len(wheel), sha256=digest(wheel))
    source_manifest = "".join(
        f"{digest(content)}  {name}\n"
        for name, content in sorted(members.items()) if name.endswith(".py")
    ).encode()
    recipe["inventory"].update(
        files=len(members), pythonFiles=2, pythonManifestSha256=digest(source_manifest),
        metadataSha256=digest(metadata), patchedMetadataSha256=digest(after),
        wheelMetadataSha256=digest(members[prefix + "WHEEL"]),
        recordSha256=digest(members[prefix + "RECORD"]),
    )
    recipe["inventory"]["license"]["sha256"] = digest(members[prefix + "licenses/LICENSE"])
    recipe["patch"]["metadataDiffSha256"] = digest(patch)
    recipe_path = tmp_path / "recipe.json"
    recipe_path.write_bytes(json.dumps(recipe).encode())
    monkeypatch.setattr(vendor_module, "RECIPE_PATH", recipe_path)
    source = tmp_path / "upstream.whl"
    source.write_bytes(upstream)
    return recipe, source, members, prefix


def test_build_is_deterministic_preserves_payload_and_upstream_identity(
    vendor_module, wheel_fixture, tmp_path,
):
    recipe, source, original, prefix = wheel_fixture
    first, second = tmp_path / "first", tmp_path / "second"
    result = vendor_module.build_vendor_wheel(source, first)
    vendor_module.build_vendor_wheel(source, second)
    assert {item.name: item.read_bytes() for item in first.iterdir()} == {
        item.name: item.read_bytes() for item in second.iterdir()
    }
    with zipfile.ZipFile(first / recipe["output"]["filename"]) as archive:
        for name, content in original.items():
            if name in {prefix + "RECORD", prefix + "METADATA", "funasr/version.txt"}:
                continue
            assert archive.read(name.replace("1.3.9.dist-info", "1.3.9+liberty.local2.dist-info")) == content
        metadata = archive.read("funasr-1.3.9+liberty.local2.dist-info/METADATA").decode()
        assert 'Requires-Dist: oss2; extra == "oss"\n' in metadata
        assert "Requires-Dist: six>=1.16.0\n" in metadata
        assert "\n\nDescription kept byte-for-byte.\n" in metadata
        assert archive.testzip() is None
    sbom = json.loads((first / "source.cdx.json").read_bytes())
    assert sbom["specVersion"] == "1.5"
    upstream = sbom["components"][0]
    assert upstream["purl"] == "pkg:pypi/funasr@1.3.9"
    assert upstream["version"] == "1.3.9"
    assert upstream["hashes"][0]["content"] == digest(source.read_bytes())
    assert upstream["externalReferences"][0]["url"] == recipe["upstream"]["url"]
    assert sbom["dependencies"][0]["dependsOn"] == [upstream["bom-ref"]]
    assert result["productionSecurityGateEstablished"] is False
    assert result["recipe"]["sha256"] == digest(vendor_module.RECIPE_PATH.read_bytes())
    assert result["patch"]["sha256"] == digest((first / "METADATA.patch").read_bytes())
    assert result["sourceSbom"]["sha256"] == digest((first / "source.cdx.json").read_bytes())
    assert result["preservation"]["pythonFileCount"] == 2


@pytest.mark.parametrize("mutation", ["digest", "size", "archive-limit", "output-digest"])
def test_source_identity_and_output_digest_fail_closed(
    vendor_module, wheel_fixture, tmp_path, mutation,
):
    recipe, source, _, _ = wheel_fixture
    if mutation == "digest":
        content = bytearray(source.read_bytes())
        content[50] ^= 1
        source.write_bytes(content)
    elif mutation == "size":
        source.write_bytes(source.read_bytes() + b"extra")
    elif mutation == "archive-limit":
        recipe["limits"]["archiveBytes"] = len(source.read_bytes()) - 1
        vendor_module.RECIPE_PATH.write_bytes(json.dumps(recipe).encode())
    else:
        recipe["output"]["sha256"] = "0" * 64
        vendor_module.RECIPE_PATH.write_bytes(json.dumps(recipe).encode())
    output = tmp_path / "output"
    with pytest.raises(vendor_module.VendorWheelError):
        vendor_module.build_vendor_wheel(source, output)
    assert not output.exists()


@pytest.mark.parametrize("existing", ["directory", "empty-directory", "file", "symlink"])
def test_existing_outputs_are_never_overwritten(vendor_module, wheel_fixture, tmp_path, existing):
    _, source, _, _ = wheel_fixture
    output = tmp_path / "output"
    sentinel = tmp_path / "sentinel"
    sentinel.write_bytes(b"unknown existing content")
    if existing == "directory":
        output.mkdir()
        (output / "unknown").write_bytes(b"preserve")
    elif existing == "empty-directory":
        output.mkdir()
    elif existing == "file":
        output.write_bytes(b"preserve")
    else:
        output.symlink_to(sentinel)
    with pytest.raises(vendor_module.VendorWheelError):
        vendor_module.build_vendor_wheel(source, output)
    assert sentinel.read_bytes() == b"unknown existing content"
    if existing == "directory":
        assert sorted(item.name for item in output.iterdir()) == ["unknown"]
        assert (output / "unknown").read_bytes() == b"preserve"
    elif existing == "empty-directory":
        assert list(output.iterdir()) == []
    elif existing == "file":
        assert output.read_bytes() == b"preserve"
    else:
        assert output.is_symlink()


def test_input_links_and_special_files_are_rejected(vendor_module, wheel_fixture, tmp_path):
    recipe, source, _, _ = wheel_fixture
    linked = tmp_path / "linked.whl"
    linked.symlink_to(source)
    with pytest.raises(vendor_module.VendorWheelError, match="regular file"):
        vendor_module.read_source(linked, recipe)
    if hasattr(os, "mkfifo"):
        fifo = tmp_path / "fifo"
        os.mkfifo(fifo)
        with pytest.raises(vendor_module.VendorWheelError, match="regular file"):
            vendor_module.read_source(fifo, recipe)


@pytest.mark.parametrize("name", [
    "../outside.py", "/outside.py", "funasr/../../outside.py", "funasr/./test.py",
    "funasr//test.py", "funasr\\test.py", "C:/test.py", "funasr/NUL.py",
    "funasr/trailing./file.py", "funasr/control\x01.py", "funasr/colon:stream.py",
    "funasr/zero\x00.py",
])
def test_unsafe_archive_paths_are_rejected(vendor_module, wheel_fixture, name):
    recipe, _, original, _ = wheel_fixture
    with pytest.raises(vendor_module.VendorWheelError, match="path"):
        vendor_module.safe_name(name)
    members = dict(original)
    members[name] = b"untrusted"
    recipe["inventory"]["files"] += 1
    with pytest.raises(vendor_module.VendorWheelError):
        vendor_module.read_wheel(encode_zip(members), recipe)


@pytest.mark.parametrize("case", ["duplicate", "case-collision", "symlink", "fifo", "directory", "extra", "root"])
def test_unsafe_zip_entries_are_rejected(vendor_module, wheel_fixture, case):
    recipe, _, members, _ = wheel_fixture
    pairs = list(members.items())
    modes, extras = {}, {}
    if case == "duplicate":
        pairs.append(pairs[0])
    elif case == "case-collision":
        pairs.append(("funasr/MODEL.py", b"collision"))
    elif case == "root":
        pairs.append(("other.dist-info/METADATA", b"wrong distribution"))
    elif case == "extra":
        extras["funasr/model.py"] = b"\xfe\xca\x00\x00"
    else:
        modes["funasr/model.py"] = {"symlink": stat.S_IFLNK, "fifo": stat.S_IFIFO, "directory": stat.S_IFDIR}[case] | 0o644
    recipe["inventory"]["files"] = len(pairs)
    with pytest.raises(vendor_module.VendorWheelError):
        vendor_module.read_wheel(encode_zip(pairs, modes=modes, extras=extras), recipe)


@pytest.mark.parametrize("limit,value", [
    ("entries", 1), ("entryBytes", 1), ("totalBytes", 1), ("compressionRatio", 0.1),
])
def test_zip_resource_limits_are_enforced(vendor_module, wheel_fixture, limit, value):
    recipe, source, _, _ = wheel_fixture
    recipe["limits"][limit] = value
    with pytest.raises(vendor_module.VendorWheelError, match="limit"):
        vendor_module.read_wheel(source.read_bytes(), recipe)


@pytest.mark.parametrize("mutation", ["truncated", "trailing", "crc", "local-path", "encrypted", "unsupported-compression", "entry-count"])
def test_corrupted_or_unsupported_archives_fail(vendor_module, wheel_fixture, mutation):
    recipe, source, members, _ = wheel_fixture
    content = bytearray(source.read_bytes())
    if mutation == "truncated":
        content = content[:-15]
    elif mutation == "trailing":
        content += b"untracked trailer"
    elif mutation == "unsupported-compression":
        content = encode_zip(members, method=zipfile.ZIP_BZIP2)
    elif mutation == "entry-count":
        struct.pack_into("<HH", content, len(content) - 14, 1, 1)
    else:
        central = content.index(b"PK\x01\x02")
        if mutation == "crc":
            content[central + 16] ^= 1
        elif mutation == "encrypted":
            content[central + 8] |= 1
        else:
            filename_length, extra_length = struct.unpack_from("<HH", content, 26)
            assert filename_length and extra_length == 0
            content[30] ^= 1
    with pytest.raises((vendor_module.VendorWheelError, zipfile.BadZipFile)):
        vendor_module.read_wheel(bytes(content), recipe)


@pytest.mark.parametrize("mutation", [
    "missing", "duplicate", "unknown", "digest", "size", "self-hash", "columns", "path",
])
def test_record_must_authenticate_every_member(vendor_module, wheel_fixture, mutation):
    recipe, _, members, prefix = wheel_fixture
    rows = list(csv.reader(io.StringIO(members[prefix + "RECORD"].decode(), newline="")))
    if mutation == "missing":
        rows.pop(0)
    elif mutation == "duplicate":
        rows.append(rows[0])
    elif mutation == "unknown":
        rows[0][0] = "funasr/unknown.py"
    elif mutation == "digest":
        rows[0][1] = "sha256=" + "A" * 43
    elif mutation == "size":
        rows[0][2] = "999"
    elif mutation == "self-hash":
        rows[-1][1] = "sha256=" + "A" * 43
    elif mutation == "columns":
        rows[0].append("unexpected")
    else:
        rows[0][0] = "../outside.py"
    stream = io.StringIO(newline="")
    csv.writer(stream).writerows(rows)
    members[prefix + "RECORD"] = stream.getvalue().encode()
    recipe["inventory"]["recordSha256"] = digest(members[prefix + "RECORD"])
    with pytest.raises(vendor_module.VendorWheelError, match="RECORD|path"):
        vendor_module.read_wheel(encode_zip(members), recipe)


@pytest.mark.parametrize("mutation", [
    "name", "version", "duplicate-version", "wheel-tag", "wheel-purelib", "wheel-version",
    "missing-record", "source", "license", "version-file", "patch-precondition",
])
def test_metadata_license_and_source_constraints(vendor_module, wheel_fixture, mutation):
    recipe, _, members, prefix = wheel_fixture
    if mutation in {"name", "version", "duplicate-version"}:
        metadata = members[prefix + "METADATA"]
        if mutation == "name":
            metadata = metadata.replace(b"Name: funasr", b"Name: different")
        elif mutation == "version":
            metadata = metadata.replace(b"Version: 1.3.9", b"Version: 9.9.9")
        else:
            metadata = metadata.replace(b"Version: 1.3.9", b"Version: 1.3.9\nVersion: 1.3.9")
        members[prefix + "METADATA"] = metadata
        recipe["inventory"]["metadataSha256"] = digest(metadata)
    elif mutation.startswith("wheel-"):
        before, after = {
            "wheel-tag": (b"py3-none-any", b"cp311-none-any"),
            "wheel-purelib": (b"true", b"false"),
            "wheel-version": (b"Wheel-Version: 1.0", b"Wheel-Version: 2.0"),
        }[mutation]
        members[prefix + "WHEEL"] = members[prefix + "WHEEL"].replace(before, after)
        recipe["inventory"]["wheelMetadataSha256"] = digest(members[prefix + "WHEEL"])
    elif mutation == "missing-record":
        del members[prefix + "RECORD"]
        recipe["inventory"]["files"] -= 1
    elif mutation == "source":
        members["funasr/model.py"] += b"CHANGED = True\n"
    elif mutation == "license":
        members[prefix + "licenses/LICENSE"] = b"removed license"
    elif mutation == "version-file":
        members["funasr/version.txt"] = b"9.9.9\n"
    else:
        recipe["patch"]["dependencyBefore"] = "Requires-Dist: nonexistent\n"
        with pytest.raises(vendor_module.VendorWheelError, match="precondition"):
            vendor_module.patch_members(members, recipe)
        return
    if mutation != "missing-record":
        del members[prefix + "RECORD"]
        members[prefix + "RECORD"] = encode_record(members, prefix)
        recipe["inventory"]["recordSha256"] = digest(members[prefix + "RECORD"])
    with pytest.raises(vendor_module.VendorWheelError):
        vendor_module.read_wheel(encode_zip(members), recipe)


def test_partial_publication_failure_cleans_only_its_files(vendor_module, tmp_path, monkeypatch):
    original_open = Path.open
    output = tmp_path / "output"

    def failing_open(filename, *args, **kwargs):
        if filename.name == "second":
            raise OSError("simulated write failure")
        return original_open(filename, *args, **kwargs)

    monkeypatch.setattr(Path, "open", failing_open)
    with pytest.raises(OSError, match="simulated"):
        vendor_module.publish(output, {"first": b"first", "second": b"second"})
    assert not output.exists()


def test_publication_collision_preserves_the_unknown_file(vendor_module, tmp_path, monkeypatch):
    original_open = Path.open
    output = tmp_path / "output"

    def colliding_open(filename, *args, **kwargs):
        if filename.name == "second" and args == ("xb",):
            with original_open(filename, "xb") as stream:
                stream.write(b"unknown concurrent file")
        return original_open(filename, *args, **kwargs)

    monkeypatch.setattr(Path, "open", colliding_open)
    with pytest.raises(FileExistsError):
        vendor_module.publish(output, {"first": b"first", "second": b"second"})
    assert not (output / "first").exists()
    assert (output / "second").read_bytes() == b"unknown concurrent file"


def test_cli_requires_explicit_paths_and_rejects_identity_mismatch(
    vendor_module, wheel_fixture, tmp_path, capsys,
):
    _, source, _, _ = wheel_fixture
    with pytest.raises(SystemExit) as error:
        vendor_module.main([])
    assert error.value.code == 2
    source.write_bytes(b"invalid input")
    assert vendor_module.main([
        "--upstream-wheel", str(source), "--output-dir", str(tmp_path / "output"),
    ]) == 1
    assert "size mismatch" in capsys.readouterr().err
    assert not (tmp_path / "output").exists()


def test_checked_in_recipe_retains_real_upstream_and_historical_candidate(repository_root):
    recipe = json.loads((repository_root / "python/funasr-runner/vendor/funasr-1.3.9.json").read_bytes())
    assert recipe["upstream"]["sha256"] == "3a2be55d6b3abc24ebb5e537789f321076df3077b000ad29530d52b3c73eed72"
    assert recipe["upstream"]["url"].startswith("https://files.pythonhosted.org/packages/")
    assert recipe["upstream"]["purl"] == "pkg:pypi/funasr@1.3.9"
    assert recipe["output"]["sha256"] == "38919720a30ab86428e2aded9a9e12cd9800cc36ebebc17f5b6b33182364f78e"
    assert recipe["inventory"]["pythonFiles"] == 417
    assert recipe["upstream"]["gitCommit"] is None
