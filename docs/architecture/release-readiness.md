# Release Readiness

Maintained against repository policy and source on 2026-11-28. This page defines the current delivery requirements and unresolved acceptance boundaries; it is not a log of historical test runs or evidence that the working tree has passed them.

Use the [development guide](../development.md) for commands, [CI/CD rules](ci-cd.md) for workflow execution and publication recovery, and [Native guide](../../apps/native/README.md) for profiles and backups.

## Native Candidate and Delivery

The workspace is preparing `2.0.0-alpha.4`; its source scope, schema v13 migration and intended installer bundle are documented in the [alpha.4 preparation notes](../releases/2.0.0-alpha.4.md). Subsequent work belongs to [Unreleased](../../CHANGELOG.md#unreleased). Published alpha.1–alpha.3 retain their original assets; source preparation does not establish a new public release.

`apps/native` is the daily development and launch entry. `apps/desktop` remains the temporary production release entry until cutover. Candidate packages retain `Liberty Native Candidate`, the separate `com.westng.liberty.native-candidate` profile and `productionReady:false`.

| Delivery path | Output and requirements |
| --- | --- |
| `pnpm native:package` | Unsigned, unnotarized candidate archives; macOS `.app` or Windows portable directory. Verify file inventory, architecture, version, manifests and SHA-256. |
| `pnpm native:installers:build` | Community macOS DMG with ad-hoc signing, or Windows per-user MSI without trusted publisher signing. Consumes verified candidate inputs on the matching host. |
| `pnpm native:distribution:build` | Optional Developer ID/notarized macOS DMG or Authenticode-signed Windows MSI. Certificates, full gates and independent verification are mandatory. |

Community installers report `signed:false`, `notarized:false` and explicit `signature:adhoc` or `signature:none`; `signed` means trusted publisher signing. They may trigger OS security prompts. Missing certificates do not block the community delivery scope, but cannot be bypassed when building the optional signed format.

Installer preparation alone is non-distributable. Installer build and verification mount or extract the package, compare embedded bytes and source provenance, and execute the contained program on the target host. This does not prove installation, upgrade, uninstall or database rollback. Uninstall must retain meetings, models, credentials and profile directories.

Updates are user initiated. The Native update window reads the fixed repository's Releases API and compares semantic versions and channels; only the download action opens a release page. There is no background download, installer execution or self-replacement. Quit before replacing the app or running an upgrade. Runtime/model downloads remain a separate first-use operation.

## Cutover and Retirement

Before switching, all sources needed by Native must live in `apps/native`, shared packages or the Python Runner: migrations, schemas, runtime/model manifests, templates, messages, artwork and sanitized compatibility fixtures. A clean checkout must regenerate and pass Native checks without reading `apps/desktop` as a source.

Cutover requires independent acceptance of:

| Gate | Required evidence |
| --- | --- |
| Business behavior | Import/record, queue/retry/delete, speakers, playback/correction, manual minutes, optional AI and exports; durable results after reopening and no automatic paid calls |
| UI and accessibility | Chinese/English, light/dark, all relevant states, keyboard/IME, screen readers, native dialogs, auxiliary-window focus/close and unsaved edits |
| Runtime and platforms | Fresh installation, fixed models and actual execution on each promised target and minimum OS; CPU and all child-process memory included |
| Quality and supply chain | Complete quality gate, real security and license scans, hash-locked inputs, source identity and reviewed distribution inventories |
| Data recovery | Profile locks, sequential schema upgrades, failed migration/retry, credential rotation, snapshot restore and interruption recovery |
| Upgrade and rollback | Transactions for the selected delivery format, retained previous installer/archive and compatible data snapshot; installer rollback alone is insufficient |
| Production observation | Explicitly reviewed acceptance evidence and observation of the candidate selected for promotion |

After all gates pass, perform one reviewed change that switches the launch channel/profile, bundle identity, input-manifest policy and production release entry; installer UpgradeCodes also change when delivering installers. Run startup, migration, lock, recovery and rollback smoke tests against that exact candidate.

The same change removes the React/Tauri/Rust GUI, capabilities, commands, old build/release chain and dependencies or checks used only by that client. Preserve SQLite upgrades, shared resources and frozen compatibility inputs needed to read existing data. The old client is a temporary migration safeguard, not a permanent second release track.

## Required Platforms

| Platform | Manifest ID | Go target | Retained Rust target |
| --- | --- | --- | --- |
| macOS Apple Silicon | `darwin-aarch64` | `darwin/arm64` | `aarch64-apple-darwin` |
| macOS Intel | `darwin-x64` | `darwin/amd64` | `x86_64-apple-darwin` |
| Windows x64 | `windows-x64` | `windows/amd64` | `x86_64-pc-windows-msvc` |

Keep platform assets and runtime assumptions distinct. Compilation and headless target tests do not establish real native interaction, managed ASR or installation on target devices.

## Quality and Evidence Gates

| Check | What it covers |
| --- | --- |
| `pnpm native:verify` | Go/generated sources, shared Python lint/tests and Native version/platform/security-baseline/lock/delivery-policy checks; no old UI or Rust |
| `pnpm check` | Native verification plus temporary production-client checks, Web build, Rust Clippy, release and CI policy; shared Python runs once |
| `pnpm native:compat` | Verified frozen historical inputs replayed through Go, including migration, business persistence and document compatibility |
| Go `-race` | Concurrency and lifecycle checks, separately from ordinary tests |
| `pnpm native:build:matrix` | Main and test compilation for all three targets |
| `pnpm security:check`, `pnpm licenses:check` | Actual dependency scanners and license policy for the repository |
| `pnpm native:security:check`, `pnpm native:licenses:check` | Explicit Native supply-chain mode without Cargo |
| `pnpm native:licenses`, `pnpm native:sbom:generate` | Reviewed Go notices and Native CycloneDX inventory |
| Dedicated acceptance commands | Real system credentials, runtime, meeting GUI, remote service, portable update/restore and controlled ASR evidence |

`pnpm check` does not replace real vulnerability scans, complete compatibility/race/matrix runs, device interaction, signing or publication verification. A missing scanner or failed scan is not a clean result.

PR/main checks select suites by changed paths. High-risk changes, manual full checks and releases use `ci-full.yml`, including both clients until retirement, compatibility, race, target execution, isolated credentials, security and inventories. No scheduled runs or ordinary-commit candidate packaging are configured. Full details are in [CI/CD rules](ci-cd.md).

## Runtime and Supply Chain

The [runtime manifest](../../packages/desktop-resources/runtime/runtime-manifest.json) and [model registry](../../packages/desktop-resources/runtime/models.json) are the version and asset authorities. Current defaults are runtime `2026.11.22-python311-security.1`, Python `3.11.18` and FunASR `1.3.9+liberty.local2`. Platform locks retain hashes.

Production installation verifies the fixed upstream wheel and reproduces the five authenticated vendor outputs before hash-locked pip installation. The [vendor recipe](../../python/funasr-runner/vendor/README.md) retains upstream identity, patch/source/output digests and licenses. The metadata patch moves `oss2` to an explicit extra and adds `six`; local package naming must not hide upstream advisories.

`security-candidate` and `funasr-upgrade` remain explicit isolated profiles. Their locks, sidecar inputs and private runtime roots are validated independently; missing or changed inputs fail instead of falling back. Bundled input files do not make installation fully offline. See [Native runtime preparation](../../apps/native/README.md#运行时与安全候选) and [FunASR upgrade](../live-translation.md#funasr-升级候选).

As recorded in the current [Python exception policy](../../python/funasr-runner/osv-scanner.toml), 20 Torch-related exceptions expire on **2026-11-30**. This is a policy-file observation, not a new scan result. Each release must rerun the pinned scanners, retain filtered and unfiltered findings, and reject expired exceptions. A policy-filtered pass does not mean no vulnerabilities; overlapping platform inventories must not be summed as distinct findings.

The scanner versions come from [dependency governance](../../config/dependency-governance.json). Native SBOM generation includes locked Go, production/development Python and root Node tooling, with FunASR upstream provenance. It excludes the old desktop importer and Rust, and is not the installed inventory of interpreters, ffmpeg, models or all native libraries. Full CI keeps Cargo coverage until retirement.

Current locks retain Torch 2.2.2; the pinned Transformers 5.x Torch backend requires a newer Torch and is disabled. Supported model evidence must cover the four fixed FunASR models, not arbitrary Transformers models. An Intel runtime replacement requires reproducible sources, native-library licenses, pinned delivery, installed inventory and real device tests; an isolated experiment is not approval to change the default.

Before delivery:

- Probe configured Python/ffmpeg URLs and verify actual bytes against SHA-256. A fixed digest rejects replacements but does not guarantee endpoint availability.
- Check the exact CPython version before pip, dependency integrity, offline model warmup and the model directory. Startup must detect an interpreter replaced in place and rerun its version probe.
- Validate each target's actual installation, minimum OS, media conversion and rollback. Reusing an installed cache is not fresh-installation evidence.
- Review runtime binaries, translation engines/models and their native dependencies separately; preserve all required licenses, attribution and notices.
- For ASR changes, use the [controlled-media contract](../../benchmarks/asr/README.md): fixed media and annotations, CER/WER/DER, failures, latency, full process-tree resources and matching commit/model/device identities. 8 GiB is a performance evidence tier, not an established minimum requirement.

## Data, Credentials and Recovery

Candidate profiles are independent. `--default-profile` is read-only; existing production data is imported offline into a new Native directory. Import takes a WAL-consistent snapshot from a quiescent source, migrates the target and rebinds active credentials without deleting source references. It does not copy media or installed runtime files.

Restore accepts a validated same-directory snapshot under the profile lock and preserves a pre-restore safety copy. Old binaries must not write an upgraded schema. Use a compatible pre-upgrade snapshot or the retained old profile for rollback; current Native changes are not reverse-synchronized.

API keys and tokens stay in the system store. Rotation retains references needed by live records and restorable snapshots, with durable cleanup intent. Restore preserves credential references in a versioned external record across SQLite replacement, and completes validation, FULL commit, WAL checkpoint and file synchronization before removing it. Invalid snapshots block cleanup. A failure after restore writing starts does not guarantee unchanged database bytes.

Test interruption, retry, blocked checkpoints, lock release and delayed credential reclamation. Never delete the database, lock, recovery record or credentials to make a test pass. Old distributed clients do not gain the current recovery protocol retroactively. Detailed operational rules remain in the [Native guide](../../apps/native/README.md#生产资料接管与离线恢复).

## Native Workflow Acceptance

Use synthetic or explicitly approved test data and the exact candidate path. Confirm the process and native window, then validate observable results and persistence:

- Dashboard aggregates, queue paging and source-scoped detail/result windows; completed and active/failed jobs follow their intended navigation.
- Batch partial failures and retry without duplicates; playback bounds, correction conflicts, independent manual notes, source links and default versus historical AI export.
- `completed`, `unavailable`, `failed` and `legacy_unverified` speaker states remain distinct. Missing diarization never invents labels in UI, AI input or exports.
- Window capabilities reject revoked or changed source/job identities. Endpoint/token changes invalidate remote grants; slow remote reads do not block unrelated window close.
- Legacy import preserves existing tasks and durable deletion intent; conflicting summary identity rolls back the batch. Compatibility inputs remain frozen.
- Cancelled, failed or revoked exports preserve the prior file and award no reward; old unstructured summaries retain their body and identity.
- Settings and models expose progress, validation, repair and failure; queued saves apply successful state and retain later failed drafts.
- Live capture handles permissions, refusal/recovery, device changes, microphone/system mixing, bounded queues, pause/end and child-process cleanup. Test numeric/name/term translations and long sessions.
- Exercise keyboard, IME, screen readers, native dialogs, focus, unsaved edits and close on each target. Component accessibility trees do not replace screen-reader operation.
- Companion animation, multi-window behavior, maps and optional assembly care remain independent of meeting progress and economic reward computation.
- Diagnostics include useful platform/schema/runtime state without keys, tokens, transcript content or user media paths. Remote upload remains unavailable until its protocol is implemented and tested.

## Publication and Installer Verification

`build-native.yml` waits for the complete gate, builds selected targets, verifies extracted executable versions and exact archive contents, then creates and validates community installers. It has no public Release or Git-ref write step.

`release-native.yml` is dispatched from main with a successful three-platform candidate run. It verifies the candidate source belongs to main history, reruns that exact source's complete gate, checks original artifact ZIP digests, and stages the bundle before granting only the publish job write permission.

The current installer-inclusive bundle contains **19 assets**: three archives, six platform manifests, three installers, three installer manifests, Native SBOM, notices, release manifest and `SHA256SUMS`. Older 13-asset releases remain unchanged. Never supplement an old tag with binaries from another source commit.

Publication binds the returned draft Release ID, rejects overwrites/unknown assets, verifies real uploaded bytes before public release, then checks unauthenticated downloads, the resolved tag commit, bot author, prerelease status and stable latest. Native 2.x prereleases use `prerelease:true` and `make_latest:false`; the old Tauri publishing path accepts only stable 1.x. Transaction recovery and explicit manual-to-bot replacement are governed by [CI/CD publication rules](ci-cd.md#native-bot-发布事务), including immutable backup and fresh-gate requirements.

Optional signed distribution additionally requires:

- macOS: `LIBERTY_MAC_SIGN_IDENTITY`, `LIBERTY_MAC_TEAM_ID`, `LIBERTY_MAC_NOTARY_PROFILE`; verify Developer ID, notarization and Gatekeeper on the actual host.
- Windows: `LIBERTY_WINDOWS_SIGN_THUMBPRINT` for a usable CurrentUser certificate and the fixed WiX toolchain; verify Authenticode and embedded provenance.
- `native:distribution:accept-install`: an older signed MSI and isolated matching host; exercise failed upgrade rollback, successful upgrade, downgrade refusal, uninstall and older-installer recovery. Database rollback is a separate gate.

Credential variables identify system-held credentials; private keys or passwords do not belong in source or logs. Preparation, building, publishing and production promotion are distinct actions.

## Outstanding Acceptance

Full production cutover remains open. Required evidence still includes complete GUI/IME/screen-reader coverage; Intel/Windows managed ASR and native interactions; external remote services; microphone/system-audio permissions and device recovery; controlled recognition/translation quality and 8 GiB resource budgets; fresh installation and selected-format upgrade/rollback; and production observation.

The [active meeting plan](../ai/plans/2026-11-28-meeting-workflow-improvements.md) retains its dated failures and incomplete checks. The [3D plan](../ai/designs/2026-11-25-native-companion-interaction-3d.md) covers separate companion integration and performance work. Neither source availability, historical logs nor documentation cleanup closes these gates.
