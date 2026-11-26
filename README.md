<p align="center">
  <img src="https://avatars.githubusercontent.com/u/277389313?s=200&v=4" width="128" height="128" alt="Liberty">
</p>

<h1 align="center">Liberty</h1>

<p align="center">A local-first desktop workspace for meeting media processing</p>

[English](./README.md) | [简体中文](./README.zh-CN.md)

Liberty brings local transcription, speaker diarization, AI summaries, meeting minutes and exports into one desktop workflow, with a separate local companion and work-game system.

## Refactor Status

The **v2.0.0-alpha.1** Native prerelease was published on 2026-11-24 with candidate archives for macOS Apple Silicon, macOS Intel and Windows x64. See the [alpha notes](docs/releases/2.0.0-alpha.1.md) for scope, profile isolation and known limitations.

As of 2026-11-25, **the new implementation is Go + MyGo under `apps/native`; `pnpm start` (equivalent to `pnpm native:launch`) starts an already packaged native candidate without compiling source.** The table below describes the current migration roles; the native client remains a candidate, and neither full UI acceptance nor production cutover is complete. The React/Tauri client is frozen only as a temporary production and migration reference. Once the Native gates close, one reviewed cutover retires and deletes that client and its release chain.

| Implementation | Role |
| --- | --- |
| `apps/native` | Go/MyGo native client; only daily development, verification and candidate packaging entry |
| `apps/desktop` | Frozen React/Tauri migration reference and current production entry until Native cutover; scheduled for deletion with its release chain after all gates pass |
| Python Runner / SQLite / shared schemas | Shared transcription, data and resource sources |

See the [native module guide](apps/native/README.md) for profiles, backup and restore. Full UI, IME/accessibility, external-service, Intel/Windows device, supply-chain and upgrade/rollback acceptance remain separate requirements. This closeout delivers unsigned, unnotarized candidate archives; signing and notarization are outside its completion conditions. Candidate identity and the other acceptance requirements remain in force; see [release readiness](docs/architecture/release-readiness.md).

## Capabilities

- **Meeting workflow:** dashboard metrics and trends, single-file local jobs, queue search/filtering, progress and logs, retry and deletion.
- **Result workbench:** dedicated windows for transcripts, verified-speaker filtering/renaming, AI summaries and read-only minutes. Missing diarization preserves the transcript without inventing speaker labels.
- **AI summaries:** OpenAI-compatible models, templates, durable runs and chunks, explicit retry and historical result selection. Users initiate summarization; transcription does not trigger it automatically.
- **Exports and members:** transcript TXT, notes Markdown, bundled Markdown and formal DOCX; Excel member import/export, departments, ordering and recorder metadata.
- **Settings:** appearance, Chinese/English, themes, runtime overview, processing defaults, Python/ffmpeg/models, remote compatibility and diagnostics. Native Settings and main navigation share color styling.
- **Resources:** model, template and member cards with dedicated editors; keys and tokens remain in the system credential store.
- **Local companion:** multiple pet windows, 255-level growth, LP, store/inventory, check-in/makeup, free daily blind boxes, gift boxes, signed redemption, farm and work maps. Factory care can optionally use assembly practice; the original one-click action remains and scores never affect rewards.

Companion features are best-effort and must not block meeting work. LP, boxes and redemption do not introduce payments, top-ups, trading or leaderboards. See the [pet system](docs/pet-system.md).

## Modes and Boundaries

Native source supports live Chinese/English microphone transcription and offline Chinese–English translation with automatic direction switching, without third-party services or API keys, plus history editing, translation retries and bilingual subtitle export; audio is not saved. See [live translation](docs/live-translation.md) for usage and current limits. FunASR 1.4.29 is evaluated in an isolated `funasr-upgrade` profile and has not replaced the default runtime.

The `processingMode` setting selects local or remote operation. `backendUrl` configures a remote connection; a nonempty address does not switch modes automatically.

Local mode uses SQLite, managed or explicitly configured Python, ffmpeg and model assets. The default runtime pins Python 3.11.18 and a hash-verified FunASR dependency patch; the shared [runtime-manifest.json](packages/desktop-resources/runtime/runtime-manifest.json) defines exact versions, platforms and downloads. The explicit `security-candidate` profile remains isolated. Updating default dependencies does not establish Native production cutover. The four pinned FunASR models and remaining Torch security exceptions are described in [release readiness](docs/architecture/release-readiness.md). Configured concurrency is a resource upper bound; 8 GiB devices default to one local job.

Remote mode negotiates `liberty-meeting` v1 operations for listing, details, results, retry, deletion and speaker renaming. Remote upload/creation and AI-summary writes are not exposed by the current client. An operation appearing in a protocol enum does not establish UI support.

AI models use OpenAI-compatible services under trusted-address, DNS, redirect and response-size constraints. API keys and remote tokens use macOS Keychain / Windows Credential Manager; SQLite stores references.

## Architecture and Layout

| Layer | Native implementation |
| --- | --- |
| UI and windows | MyGo native rendering |
| Application and domain | Go application/domain/client |
| SQLite | `modernc.org/sqlite`, typed repositories |
| Platform services | Go profile/credentials/system adapters |
| Shared sources | JSON Schema, Python Runner, runtime manifest, DOCX template, messages and assets |

```text
apps/native/                  Go + MyGo candidate
  cmd/liberty-native/         Launch options and channel
  internal/desktop/           Windows, layouts and native dialogs
  internal/client/            UI controllers and asynchronous state
  internal/application/       Use cases, ports, scheduling and scopes
  internal/domain/            Types and business policies
  internal/infrastructure/    SQLite, runtime, HTTP, files and platform adapters
  internal/host/              Composition, startup recovery and shutdown
apps/desktop/                 Temporary production client and migration reference
packages/desktop-resources/   Shared sources, artwork and frozen synthetic regression fixtures
packages/shared-types/        Versioned JSON Schema and generated types
python/funasr-runner/          Runner, validators and platform dependency locks
scripts/                      Development, generation, acceptance and packaging
benchmarks/asr/                ASR fixture and platform-evidence contracts
config/                       Platform, security and license policies
docs/                         Maintained guides and historical process records
```

The database is schema v12, including live-session/caption tables and automatic translation direction. Native generates migration definitions from shared sources and replays the historical Rust implementation's frozen synthetic databases and expected outputs in `packages/desktop-resources/fixtures/native-v10`; it no longer builds or runs the Rust oracle. Before cutover, a clean checkout must regenerate and test without reading `apps/desktop`. See [native architecture](docs/architecture/native-desktop-architecture.md) for module boundaries and source mappings.

## Development and Verification

Use pinned `pnpm@10.30.3`, Go `1.27.4` and Rust `1.94.0`. Environment and command details are in the [development guide](docs/development.md).

```sh
pnpm install --frozen-lockfile
pnpm native:check
pnpm native:package
pnpm start
```

`start` and `native:launch` verify packaged files and select the highest SemVer for the current platform, using package-directory modification time to break ties. They do not rebuild source, stop running processes or launch the old React/Tauri client; a different running native executable path blocks launch. On macOS they use the exact `.app` path and activate an existing instance of that build. The Windows launcher does not implement existing-window activation; bring a running candidate to the foreground instead of launching again. Root aliases that could start the old UI are removed; explicit old-client maintenance uses its directory scripts, as described in the [development guide](docs/development.md). Candidates use a separate profile by default. macOS candidates are unsigned/unnotarized; Windows packages are portable directories. Exit normally before loading a new build; running windows do not reload automatically.

Synthetic preview and compatibility checks:

```sh
pnpm native:preview --writable --theme light
pnpm native:compat
pnpm native:build:matrix
```

`native:preview` first verifies and replays the shared frozen golden fixtures through Go, then opens temporary synthetic data and cleans it on exit. It does not run the old client. This is not full UI or production acceptance. `native:build:matrix` establishes three-target compilation, not execution on other platforms.

For cross-layer changes and shared-source checks:

```sh
pnpm check
```

`pnpm check` runs Native and temporary production-client checks, with shared Python checks executed once. PR and main CI select checks by changed paths; high-risk changes, manual full validation and releases run the complete gate. There are no scheduled runs or automatic candidate packages on ordinary commits. The manual `build-native.yml` workflow requires the complete gate before packaging and verifying the selected targets. See [CI/CD rules](docs/architecture/ci-cd.md) and the [development guide](docs/development.md) for compatibility, race, security, device and release acceptance. Legacy checks are removed with the old client in the retirement change.

## Platforms

| Platform | Go target | Candidate package argument | Runtime boundary |
| --- | --- | --- | --- |
| macOS Apple Silicon | `darwin/arm64` | `darwin-arm64` | FunASR; local evidence is limited to tested scenarios |
| macOS Intel | `darwin/amd64` | `darwin-x64` | Independent runtime and UI device acceptance required |
| Windows x64 | `windows/amd64` | `windows-x64` | Independent runtime and UI device acceptance required |

## Documentation

- [Documentation index](docs/README.md): maintained guides, module references and archives.
- [Development guide](docs/development.md): launch, checks, generation and acceptance.
- [Native architecture](docs/architecture/native-desktop-architecture.md).
- [Native candidate guide](apps/native/README.md) / [release readiness](docs/architecture/release-readiness.md).
- [Pet system](docs/pet-system.md) / [process-document index](docs/ai/README.md).

## License

Liberty is licensed under the [MIT License](LICENSE). Dependencies and runtime assets retain their own licenses, notices and distribution requirements.
