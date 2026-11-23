# Release Readiness

Last verified: 2026-11-23 against the current checkout, root `package.json` scripts and pinned dependency scanners. Security-scan findings below retain their stated scan dates.

Use the [documentation index](../README.md), [development guide](../development.md)
and [native architecture](native-desktop-architecture.md) for navigation and
implementation details. This page defines release gates. The 2026-11-20
documentation refresh did not rerun external scans, publish a release or
approve cutover; subsequent scan runs are dated below.

## Native Migration Candidate

The production entry remains `apps/desktop`. `pnpm native:package` creates an
unsigned, unnotarized, isolated Go + MyGo candidate for the local host or an
explicit target. Candidate startup defaults to a separate profile identity;
the existing Tauri profile is read-only even with explicit `--default-profile`.
Offline `--import-profile <source> --profile-directory <new-target>` takes a
WAL-consistent SQLite snapshot from a quiescent source, migrates the independent
target and rebinds active credentials without deleting source references. It
does not copy media or installed runtimes. Import retries preserve staged
compensation evidence, keep the live credential set consistent after partial
cleanup, and never overwrite an existing target. Idle and failed runtime
operations do not block import; active work and unknown operation states do.
Restore is limited
to independent Native profiles: `--restore-backup` validates a same-directory
SQLite snapshot under the profile lock, checks referenced credentials, and
preserves a pre-restore safety snapshot. Package
compilation and archive hash verification do not prove native UI parity,
Windows/Intel execution, signing, notarization, installer upgrade/rollback,
security-scan clearance, or production readiness. Keep the current client and
profile only as a temporary migration safeguard until every cutover condition
in the approved refactor design is independently accepted; this is not a
permanent compatibility promise.

## Cutover and Retirement

The old React/Tauri client is retained only until the Native cutover gates are
closed. It must not become a second production track or an excuse to leave
unverified behavior in the migration. Before switching, move every required
fact source out of `apps/desktop`: schema and migration inputs, runtime and
model manifests, templates, messages, artwork, and compatibility fixtures must
be owned by `apps/native`, `packages/desktop-resources`, or an independent
fixture package. A clean checkout must regenerate and pass the Native checks
without reading the old client as a source.

After all business, UI/accessibility, platform, security, data-recovery,
installer upgrade/rollback, signing, notarization, and production-observation
gates pass, perform one reviewed cutover change that:

1. switches the default production entry and release workflow to Native;
2. runs the Native startup, profile-lock, migration, recovery, and rollback
   smoke tests against the production candidate; and
3. deletes `apps/desktop` React/Tauri/Rust GUI code, Tauri capabilities and
   commands, old build/release workflows, and dependencies or checks used only
   by that client.

Keep the SQLite migration logic, shared resources, and sanitized cross-language
golden fixtures needed to read and upgrade existing user data. Do not keep an
old Tauri executable or a permanent compatibility release after this change.

Normal model/token rotation must retain credentials still referenced by a
restorable snapshot and keep a durable retirement intent for eventual cleanup.

## Independent Native Delivery

`pnpm native:verify` is the complete local Native quality entry: Go checks and
shared generated sources, Python lint/tests, Native version and four-target
policy, required security regressions, lock integrity, candidate workflow policy,
and failure fixtures. It does not invoke the old UI or Rust. The root `pnpm check`
runs it before `legacy:check`; the latter retains the old production checks only
until the retirement change above.

`.github/workflows/build-native.yml` can run manually or through the root quality
workflow. Its independent quality job also replays frozen compatibility fixtures,
runs race checks, cross-compiles all targets, collects Go notices and produces a
Native lock-inventory SBOM. Four platform jobs execute target tests and isolated
system-vault acceptance, build portable candidates, execute the extracted version
check, and verify exact archive contents, manifest, architecture and SHA-256
before uploading the candidate artifacts.

The separate real dependency-security job is required: findings or scanner
errors fail the workflow. Candidate build artifacts may remain available when
this job fails, as in the existing quality workflow, but cannot establish
production security clearance. All jobs have read-only repository permissions;
there is no Release publication, Git-ref mutation, signing or notarization step.
`native:security:check`, `native:licenses:check` and `native:sbom:generate` use
the explicit Native governance mode without Cargo. Existing production Rust
checks remain in the old production chain until its retirement.

The Native SBOM describes locked Go modules, three production Python targets,
Python development tools and the transitive root Node build tools. It retains
the FunASR upstream 1.3.9 identity alongside local2 and its dependency edge and
source/output hashes. It excludes the old desktop importer and Rust, and is not
an installed or packaged inventory of interpreters, FFmpeg or models. The
2026-11-22 regeneration contains 135 components; older 123-component artifacts
precede the production runtime changes below.

`native:distribution:check` validates the installer policy and refusal fixtures.
The distribution CLI supports prepare/build/verify and Windows accept-install.
macOS builds require `LIBERTY_MAC_SIGN_IDENTITY` (Developer ID certificate SHA-1),
`LIBERTY_MAC_TEAM_ID` and `LIBERTY_MAC_NOTARY_PROFILE` (notarytool keychain profile).
Windows builds require `LIBERTY_WINDOWS_SIGN_THUMBPRINT` for a usable CurrentUser
code-signing certificate. Values identify system-held credentials; do not put
private keys or passwords in source or logs. The build runs the full Native
quality, compatibility, race, platform, security and license gates in a clean
checkout before signing freshly built input. Signed embedded provenance binds
the exact source commit and input manifest; editing an outer JSON file cannot
relabel an installer. Windows uses the fixed official WiX 3.14.1 ZIP with its
reviewed full digest. Installer transaction checks require an older signed MSI
and an otherwise uninstalled candidate. macOS verification mounts a DMG read-only
and checks the contained application, notarization, Gatekeeper, version and
architecture. These implementations require actual certificate/platform runs;
the candidate identity and `productionReady:false` remain until cutover.
On 2026-11-22, the distribution policy and its 66 tests passed, including all four
targets' verified synthetic archive preparation, dirty-source and symlink-path
refusals, and signed-provenance claim checks. These are not signed installer
builds or installation acceptance. No certificate-backed DMG/MSI build,
notarization, Windows upgrade/uninstall transaction, or distribution CI execution
has been accepted. Promotion still requires one reviewed change to the launch
channel/profile, bundle identity, manifest policy, installer UpgradeCodes and
release entry, followed by retirement of the old client under the gates above.

Unreadable or invalid snapshots must block deletion. Restore preflight checks
existing credential references in early schemas as well as current schemas.
Explicit restore preserves live and pending credential references in a strict,
versioned external record before replacing SQLite. Importing retirement intents
must retain existing metadata and finish a FULL commit, WAL checkpoint and
main-file synchronization before removing that record, even on a no-op replay.
Interrupted import and blocked checkpoints must retain retry evidence. This
branch also merges snapshot-retained v3 migration journal entries on retry.

The macOS ARM isolated system-store acceptance now covers model/token rotation,
SQLite restore, reopen, pre-restore safety-snapshot retention and eventual
reclamation of only non-live credentials. Synthetic host regressions cover
business data and cleanup intent preservation. These results do not establish
physical power-loss or installation rollback. The later 2026-11-23 CI records
below extend isolated system-store acceptance to Intel Mac and Windows x64.
Old distributed clients do not gain this protocol retroactively.

## Required Platforms

Every release candidate must account for these desktop targets:

| Platform | Manifest platform ID | Rust target | Go target | Validation |
| --- | --- | --- | --- | --- |
| macOS Apple Silicon | `darwin-aarch64` | `aarch64-apple-darwin` | `darwin/arm64` | primary |
| macOS Intel | `darwin-x64` | `x86_64-apple-darwin` | `darwin/amd64` | primary |
| Windows x64 | `windows-x64` | `x86_64-pc-windows-msvc` | `windows/amd64` | primary |
| Windows x86 | `windows-x86` | `i686-pc-windows-msvc` | `windows/386` | extended; managed local ASR installation unsupported |

Windows x86 remains a supported target, but it must be validated independently
because runtime dependencies and model performance differ from x64.

## Automated Checks

Run before release:

```bash
pnpm check
```

This includes:

- complete local Native quality and candidate-delivery policy checks
- generated-contract drift checks
- frontend type checking, unit tests, and production build
- Python Ruff and pytest
- Rust formatting check
- Rust tests
- Rust clippy with warnings denied
- release version, platform-matrix, Tauri security-baseline, and runtime-lock checks
- dependency-governance and ASR-validation blocking fixtures

`pnpm check` validates the governance code and its blocking fixtures, but it does
not replace a real dependency scan, controlled-media benchmark, native platform
smoke, signing, notarization, installer test, or published-asset verification.

Native changes also require the appropriate `pnpm native:compat`, Go race checks
and `pnpm native:build:matrix`. The root quality workflow delegates the complete
Native quality and platform candidate jobs to `build-native.yml`. Matrix
compilation is not execution on those devices. Candidate packaging verifies
version, identity, architecture and archive contents; Windows portable packages
are not installers.

## External Evidence Gates

Run the real scanners in an environment with the pinned prerequisites:

```bash
pnpm security:check
pnpm licenses:check
```

For the Go migration client, `pnpm native:licenses` checks reviewed module
versions against `apps/native/go.mod`, verifies module checksums and collects
upstream notices. The supply-chain job publishes the notice file alongside
the CycloneDX artifact. OSV also scans `apps/native/go.mod`. All dependency
ecosystems must pass the security gate before switching the production client.

The historical 2026-11-19 production scan was blocking: each of the three Python locks
had 54 unsuppressed vulnerability groups (75 OSV records) under the existing
exception policy. Those counts overlap across platforms and must not be added
as distinct vulnerabilities. The 31 Python exceptions expire on 2026-11-30.
The configured Rust, pnpm and native Go lock scans and the existing Rust/Go
license gates passed in that run. An additional Go source-analysis diagnostic failed
because the pinned scanner cannot process Go 1.27 sources; this is unverified
source analysis, not a clean analysis result or a substitute for the lock scan.
The 2026-11-22 production scan below supersedes these old lock findings for the
current inputs; it does not change what the historical scans observed.

On 2026-11-21, the fixed `cargo-deny 0.20.2` and `osv-scanner 2.5.0`
release binaries were verified against their upstream release SHA-256 digests
and run from a temporary tool directory. Rust advisories/bans/sources and the
pnpm/native Go dependency scans passed. `pnpm security:check` then exited 1 on
the first production Python lock; independent scans of the other two locks
also exited 1. Each lock still reported 54 unsuppressed vulnerability groups
across three packages: `filelock`, `pillow` and `transformers` (6 critical,
29 high, 16 medium, 2 low and 1 unknown). These platform counts overlap and
must not be summed. The production locks and exception policy were unchanged;
this rerun did not repeat Go source analysis, license or runtime installation
acceptance. `pnpm check` and `pnpm native:check` passed separately and do not
clear this security gate.

A patched or vendored runtime package must retain its upstream identity for
scanning, source and patch digests, reproducible output digest, and license
notices. Verify its immutable delivery path, fresh hash-locked installation,
default model paths, minimum OS compatibility, and rollback on each promised
runtime platform. An exception-filtered scan does not resolve the underlying
vulnerabilities or extend an exception's expiration date.

The native runtime experiment has a checked-in, fixed FunASR wheel recipe and
standard-library builder; see the [vendor build instructions](../../python/funasr-runner/vendor/README.md).
It produces the candidate, patch, provenance and an upstream-identity source
SBOM. This source SBOM is not the full runtime dependency inventory. Reproducing
the candidate does not establish immutable delivery or pass the production
security and installation gates described above.

The native candidate also has an explicit, pinned sidecar delivery path. The
single runtime manifest retains its production root and declares the separate
`security-candidate` profile, Python assets and eight input digests. The
repository contains the [three candidate locks](../../packages/desktop-resources/runtime/candidates/security-candidate).
Preparation verifies those locks and the five outputs from the pinned vendor
recipe. Packaging with `--runtime-input-directory` runs the candidate security
gate before copying and revalidating those bytes. Each platform scan checks
coverage of the complete dependency inventory and the upstream FunASR source
identity. Both current-policy and unfiltered results are retained. On
2026-11-21, all eight
inputs matched their manifest digests after rebuilding the five vendor outputs.
The three candidate scans passed with the existing exceptions. Each unfiltered
scan still reported findings, and the separate production scans failed.

The candidate `filelock 4.2.1`, `Pillow 13.0.0`, and `Transformers 5.23.0`
require Python 3.11 or later. At that review date, production pinned Python
3.10.17; the 2026-11-22 production update below replaces that pin. A 2026-11-21
review of the [Torch](https://pypi.org/pypi/torch/json) and
[Torchaudio](https://pypi.org/pypi/torchaudio/json) release inventories found
2.2.2 was the latest stable release of each with a non-yanked CPython 3.11
Intel or universal2 macOS wheel. The candidate Transformers Torch backend
requires Torch 2.6 or later. Any alternative Intel build needs reproducible
provenance, license and installed inventory review, pinned delivery and
actual runtime tests. Check installation on the minimum supported macOS
version as well, since the candidate SciPy lock includes wheels with different
minimum macOS versions.

On 2026-11-20, configured archive endpoints were independently probed using
HTTPS HEAD and one-byte Range requests. The official Python/macOS FFmpeg
archives responded successfully, but all four configured Python archive URLs
on the NJU mirror and both previous Windows FFmpeg release URLs returned 404.
The dead NJU package URLs have been removed; NJU remains available as a PyPI
index and Python archive selection now falls back to the checksum-pinned
official URL.

Windows x64 and x86 FFmpeg pins were replaced with separately hosted, fixed
FFmpeg-Builds release assets. Both endpoints returned HEAD 200 and one-byte
Range 206. The retained local ZIP copies matched the recorded full SHA-256
values, passed ZIP integrity checks, and contained correctly matching x64 PE32+
and x86 PE32 executables. The x86 ASR limitation is unchanged. The upstream
assets are GPL builds, their repositories are separate, their GitHub releases
are not immutable, and the upstream retention statement is time-limited; the
SHA-256 guard rejects changed bytes but does not guarantee long-term endpoint
availability. No Windows executable was run: runtime installation, FFmpeg
version, media conversion, minimum-OS behavior, and rollback still require
Windows x64/x86 acceptance. Existing valid managed FFmpeg installations may
still be reused; changing the manifest does not by itself prove those installs
were upgraded.

The `production` runtime profile is the default; selecting `security-candidate`
remains explicit and requires an isolated writable profile.
The application locates inputs relative to its exact executable, validates them
before opening the database, and materializes the verified bytes into a private
content-addressed resource directory. Candidate generations, downloads, logs and
models use a separate profile root; ready markers bind the profile and input
digest. Missing or changed inputs fail instead of falling back. The production
installer downloads the fixed upstream wheel and reproduces the five authenticated
vendor outputs before hash-locked dependency installation. The explicit candidate
sidecar bundles its vendor wheel. Both paths still require online installation.
Minimum-OS, Intel/Windows execution and installer rollback gates still apply.

On 2026-11-20, the macOS ARM candidate completed a fresh hash-locked managed
installation, all four pinned model downloads and offline warmup, and both
speaker modes through the original Runner and real native host. Host results
and workflow rewards survived shutdown and reopening. The collected 82-package
installed inventory passed pip check, exact-lock comparison and the same
upstream-aware current-policy scan; its unfiltered scan still reported findings.
Transformers disables its Torch backend because that backend requires >=2.6;
the successful model evidence covers only the fixed FunASR path. This is neither
multi-speaker accuracy evidence nor a performance or other-platform result.

On 2026-11-21, the versioned production and `.2` security-candidate caches each
passed the pinned Runner and real host checks for both speaker modes, including
results and workflow rewards after reopening. The candidate's 82-package
installed inventory passed exact-lock comparison and the current-policy scan;
its unfiltered scan retained findings. A later candidate package repeated all
three platform input scans with complete coverage and unchanged exception
policy. Those runs did not clear the production Python findings recorded then.
The separate meeting-window acceptance was incomplete at that date after an
interrupted UI session. The 2026-11-23 interactive acceptance below supersedes
that result for its explicitly tested macOS ARM workflow.

### 2026-11-22 Production Runtime Update

The shared manifest now defaults to runtime `2026.11.22-python311-security.1`,
CPython 3.11.18 and FunASR `1.3.9+liberty.local2`. The three production locks
use the corrected dependency set, including `filelock 4.2.1`, `Pillow 13.0.0`
and `Transformers 5.23.0`. The fixed metadata patch removes `oss2` from the
default dependency set into an explicit extra and adds `six`; the upstream
FunASR identity, unchanged Python payload and source/output hashes remain
available for review in the [vendor build instructions](../../python/funasr-runner/vendor/README.md).
The managed CPython 3.11.18 build reproduced all five pinned vendor outputs.

With pinned OSV Scanner 2.5.0, all three current production lock inventories
and the 82-distribution macOS ARM installed inventory passed the existing
exception policy with complete coverage. Each corresponding unfiltered scan
exited 1 and retained 20 vulnerability groups covering 24 OSV records, all
attributed to Torch 2.2.2. The same findings overlap across the three targets and
the installed inventory; do not add them as distinct vulnerabilities. The 31
existing exceptions were not expanded or extended and still expire on
2026-11-30. These results supersede the 2026-11-19/21 findings for the old
production locks; they establish current-policy passage, not an absence of
vulnerabilities or unconditional production clearance.

On macOS Apple Silicon, a fresh default-production managed installation passed
the pinned runtime/model/audio acceptance (201.64 seconds), installed inventory
acceptance (14.05 seconds), and real Native host acceptance (78.02 seconds).
Both speaker modes completed through the application and Runner; their results
and workflow rewards survived profile shutdown and reopening. The installed
inventory matched the lock and passed the current-policy scan; its unfiltered
findings remain as described above. This run used the default production
profile rather than a successful historical candidate cache.

Transformers 5.23.0's Torch backend remains disabled because it requires Torch
2.6 or later, while Intel macOS remains pinned to its final official Torch 2.2.2
wheel. The accepted path is limited to the four pinned FunASR
Paraformer/VAD/punctuation/speaker models using Torch directly. It does not
establish support for arbitrary Transformers models. The runtime and host
results do not prove GUI meeting/summary/export acceptance, CER/WER/DER or
performance thresholds, minimum-OS behavior, Intel/Windows execution,
certificate-backed distribution, installer rollback, or production observation.
Those gates remain open, and the old production entry has not been replaced.

Scanner exit code `2` means the required tool is unavailable, not that the scan
passed. When the ASR engine, runtime, model, or supported-platform assumptions
change, also run the controlled fixture, benchmark, platform-smoke, and accepted
evidence workflow documented under `benchmarks/asr/`; missing media, annotations,
registered devices, or digest-bound evidence remains blocking.

### 2026-11-23 Source and Interactive Acceptance

Commit `5e055a6e729b5e856f1011ed4f559fa1e14abce1` was tracked and pushed to
`codex/native-architecture`, then checked out into a clean detached worktree.
Fresh frozen root Node tooling, a separate hash-locked Python development
environment, Native verification, licenses, the 135-component SBOM and Go module
verification passed without changing the checkout. A real ARM candidate archive
was built, extracted and executed with `--version`; distribution preparation
bound the exact commit and archive while retaining `distributable:false` and
false quality/security publication claims. This does not establish signing.

The macOS ARM meeting-window acceptance passed with the default production
runtime: system file selection, synthetic speech transcription into four segments
and verified speakers, summary generation against a local authenticated fixture,
saved minutes, and complete-result export through the system save dialog.
After normal application shutdown, the test verified the completed job, one
published summary, expected export text and exactly one of each workflow reward.
The final run also removed only its own temporary database and attachments.
An initial cleanup failure was reproduced as the external filesystem changing
its provisional inode on first write; the fixture now compares the current path
with its retained original file descriptor. This synthetic GUI run does not
prove external-service acceptance, human ASR quality, all-page accessibility,
IME composition, or other operating systems.

Actual CI initially found non-desktop runtime/profile test assumptions and two
new Rust advisories. `cookie` 0.18.3 and `url` 2.5.9 (with `form_urlencoded` 1.2.3)
passed a fresh cargo-deny advisories/bans/sources scan and the full legacy checks.
Linux ARM container execution verifies the corrected host/runtime tests and the
offline CLI restore against explicit synthetic directories. The first three CI
runs also exposed the frozen fixtures' original UTC+08 clock assumption and a
synthetic host runtime lacking the required production input binding. The fixes
are limited to explicit test setup; production runtime ownership and the frozen
golden bytes remain enforced.

Commit `b8ff0baf720ef8444c4e9634a36c2548b9236b2b` passed the full root
`pnpm check` and the complete frozen compatibility replay with `TZ=UTC`.
A second clean detached checkout built all four candidate archives and prepared
their installer inputs. Each archive's architecture, exact source provenance
and SHA-256 were checked; only the ARM candidate was executed locally. All
prepared inputs remain non-distributable. The checkout stayed clean.
CI run `45164966173` targeted this same commit with `publish_release=false`.
Independent Native quality, both real security jobs, and the ARM and Intel Mac
candidate jobs passed, including real system-vault recovery and extracted
candidate execution. Both Windows jobs failed. The failures exposed a missing
Windows handle-based hard-link check, invalid synthetic filenames/SQLite URIs,
and a 32-bit animation-index overflow; their fixes require another target run.

The x86 UI failures also establish a separate implementation blocker: the pinned
MyGo selects both its unsupported window backend and its stub text engine on
Windows 386. The actual `go list` selections are `backend_other.go` and
`engine_other.go`; the Windows window backend and DirectWrite are built only for
amd64/arm64. The inspected v0.10.0 release and upstream main still exclude 386
from DirectWrite. The text stub draws no glyphs and does not wrap paragraphs.
A Windows x86 compilation or `--version` result cannot establish a working GUI.
Closing this target requires a proper MyGo Windows x86 window/text backend port
and target verification; tests and the four-target release gate remain enforced.

The final implementation commit is
`22aaabbe74eb4958b86e5db7233186fd7eaeb087`. It passes a fresh full root check and
CLI/managed race checks. Unsupported GUI architectures now return bilingual
recovery guidance before profile resolution or creation; offline import/restore
and version metadata remain available. An isolated Linux process verified that
refusal without creating its requested profile. The intermediate Windows run
confirmed the hard-link, fixture-path and animation fixes, then exposed a
read-only snapshot cleanup permission issue corrected in this final commit.
The final run is [45175760469](https://github.com/whalesky-labs/Liberty/actions/runs/45175760469),
with publication disabled and a final conclusion of **failure**. Its Native
quality and both real security jobs passed. Apple Silicon, Intel Mac and Windows
x64 passed all ordinary target tests, actual isolated
system-store rotation/restore/reclamation, extracted candidate execution and
archive verification. Windows x86 passed the repaired managed, profile, host
and legacy-reader tests; its desktop package still fails eight layout/typography
tests because of the backend limitation above. Its separate system-vault command
did not run after that failure. All three successful candidate artifacts were
downloaded and independently checked against GitHub's ZIP digest, their exact
file inventories, architecture, source commit and embedded/outer SHA-256 records.
The failed x86 target blocks the full workflow; release reservation, bundle and
publication jobs were skipped. No tag or Release was created.
A fresh detached checkout of the same commit installed frozen
root tooling, packaged and executed the ARM candidate, and verified archive
SHA-256 `238121c0686dffa1260f5e0e480f6aba24a7162e4174502331953bff1e0ce6d9`.
The checkout remained clean and the candidate remains unsigned and non-production.

| Final CI candidate | Target execution and system store | Downloaded archive SHA-256 |
| --- | --- | --- |
| macOS Apple Silicon | Passed | `c40406ced76aea6f04618d7f05c058c51b21fa19d2e229f63c840621e8b22c4b` |
| macOS Intel | Passed | `18df4d1d64e2d4f282b785f39d0a11af41a500079625d4b535ec7486985a3f78` |
| Windows x64 | Passed | `2de890b9529a1fb1c545de83d26fa1242850acf1d7ab2269d21aa1fa87370de3` |
| Windows x86 | Failed in desktop tests; separate vault acceptance not reached | No accepted artifact |

These target checks do not prove native GUI interaction, Intel/Windows managed
ASR, minimum operating systems, a 32-bit Windows installation, certificate-backed
distribution, controlled ASR quality, 8 GiB performance or production observation.

Additional macOS ARM page acceptance covered English dark and Chinese light
themes, settings categories and diagnostics, member and custom-template saves,
required model fields, pet growth, one daily check-in and one blind-box draw.
The factory practice completed with keyboard input, replayed, paused with Escape
and returned without a work reward. Reopening the synthetic database verified
the saved resources and single reward records. The widened English Encourage
button was visibly verified in the later candidate; normal quit completed.
This bounded page sample does not establish every state, IME composition,
screen-reader behavior or gameplay calibration.

## Manual Desktop Workflow Checks

- Start the exact native candidate application, confirm its `liberty-native`
  process and native window, and avoid running a second instance against the
  same SQLite database.
- Confirm the dashboard loads aggregate metrics, trends, completeness, recent
  jobs, and local resource state without loading every full job result.
- Confirm completed jobs open the scoped `job-workbench` directly, while active
  or failed jobs open details; legacy result routes must redirect to the
  completed-job queue.
- Confirm a workbench can access only its bound source and job, rejects an invalid
  or replaced capability, and can request AI-summary or meeting-notes windows
  only for that same job. The trusted main shell creates auxiliary native windows
  using process-local capabilities. Cancellation during a resource lookup and resource-service
  shutdown must prevent editor authorization issuance or renewal.
- In the native candidate, change the remote endpoint or rotate the credential
  while its workbench stays open. The old grant must not access a same-ID job on
  the new connection. Keep an independent local notes window open during a slow
  remote read and confirm that closing it remains responsive. Switching only
  processing mode must preserve the original remote connection.
- Import a legacy JSON task whose ID already exists in SQLite, and one with a
  durable deletion intent. Preserve the existing result and deletion; import only
  missing tasks. A colliding summary ID must roll back the entire batch and leave
  the completion marker unset. Check both native and this branch's Rust importer.
- Export an old unstructured summary to Word twice, with a restart between
  exports. Preserve its body and source-summary identity after payload backfill;
  do not repopulate an already saved payload from stale AI text. Dismissed,
  cancelled or revoked exports must preserve the previous artifact and award no
  export reward.
- Confirm `completed`, `unavailable`, `failed`, and `legacy_unverified`
  diarization states remain distinct. Missing diarization must preserve the
  transcript and must not synthesize a speaker label in UI, AI prompts, or
  exports.
- Confirm Settings can independently display and operate Python, ffmpeg, and
  model resources, including source selection, progress, failure, repair, and
  validation states where supported.
- Compare every legacy route with the native route using the same synthetic
  records, locale, theme, viewport, and interaction state. Route-map tests prove
  navigation coverage only; they do not prove visual or behavioral parity.
- Exercise native keyboard, IME, file dialogs, auxiliary-window focus/close,
  accessibility tree and screen-reader paths on each promised desktop platform.
- Keep remote task creation explicitly unavailable until the service upload
  protocol is specified and tested. The current legacy submission path also
  reports that secure chunked upload is not implemented; a visible legacy form
  is not evidence of a successful remote create workflow.

## Manual Release Checks

- Confirm the [shared runtime manifest](../../packages/desktop-resources/runtime/runtime-manifest.json) has explicit entries for each supported platform ID, and the [model registry](../../packages/desktop-resources/runtime/models.json) pins all four default model revisions.
- Confirm configured Python and ffmpeg asset URLs are reachable with a cheap HEAD or byte-range probe before publishing them to users.
- Confirm managed installation verifies final CPython `X.Y.Z` against the manifest before pip and validates the platform lock. In Go, confirm startup rejects an interpreter replaced in place. Reconciliation checks the executable fingerprint and repeats the version probe when the fingerprint changes. System Python follows its separate capability policy.
- Confirm placeholder or unavailable runtime sources are not advertised in the client.
- Confirm macOS Intel and Apple Silicon artifacts are separate.
- Confirm Windows x86 and x64 artifacts do not share runtime assumptions.
- Confirm Windows x86 unsupported runtime-download messaging is still accurate when the platform remains listed.
- Confirm the Environment & Models settings flow can list configured download sources and surface runtime-install logs.
- Confirm diagnostics report shows the expected current platform and schema version.
- Confirm no API keys, tokens, or local source paths appear in exported diagnostics.
- Confirm desktop-pet diagnostic log export redacts sensitive paths and includes useful runtime/resource state.
