<p align="center">
  <img src="https://avatars.githubusercontent.com/u/277389313?s=200&v=4" width="128" height="128" alt="Liberty">
</p>

<h1 align="center">Liberty</h1>

<p align="center">A local-first desktop workspace for meeting media processing</p>

[English](./README.md) | [简体中文](./README.zh-CN.md)

Liberty combines local transcription, speaker diarization, transcript correction, meeting minutes and exports in a desktop application. Optional companions and work games provide a separate local activity.

The product is free. Basic meeting workflows require no API key or paid API; AI summarization is optional, starts only when requested, and may incur charges from the provider you choose. Runtime and model setup is managed by the app. CPU operation and ordinary computers are the target; minimum hardware and performance still require device measurements.

## Current Status

The workspace is preparing **2.0.0-alpha.4**. See the [fourth prerelease scope](docs/releases/2.0.0-alpha.4.md), with subsequent work under [Unreleased](CHANGELOG.md#unreleased). A source version does not establish publication; use the assets actually available on [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases).

| Implementation | Current role |
| --- | --- |
| `apps/native` | Go + MyGo + SQLite candidate; daily development, launch and candidate packaging |
| `apps/desktop` | Frozen React/Tauri implementation; temporary production release entry and migration reference |
| Shared resources and Python Runner | Runtime, models, schemas, templates, messages, artwork and transcription protocol |

Native production cutover is incomplete. After the [cutover gates](docs/architecture/release-readiness.md#cutover-and-retirement) pass, the old client and its release chain are removed together. Candidates use an independent profile; importing existing data is an explicit operation.

## Meeting and Companion Features

- **Import and process:** single files or batches of up to 32, persistent queues, progress, logs, retries and deletion. Failed batch admissions can be retried individually.
- **Listen and correct:** segment playback with speed control, text corrections, speaker reassignment and verified-speaker filtering/renaming. Unavailable diarization preserves the transcript without inventing speaker labels.
- **Write minutes:** independent manual notes with source links, optional OpenAI-compatible AI summaries, templates and saved summary history. AI completion does not overwrite manual notes.
- **Export and manage resources:** TXT, Markdown and formal DOCX; Excel member import/export, departments, recorder metadata, models and templates.
- **Live transcription and translation:** local Chinese/English microphone transcription, automatic offline translation, history editing, retries and bilingual SRT/VTT/TXT. System-audio and mixed-input adapters exist, with permissions and target-device acceptance still pending.
- **Settings:** Chinese/English, appearance, runtime/model installation, processing defaults, storage, permissions, diagnostics and user-initiated update checks.
- **Local companion:** 255-level growth, LP, store/inventory, check-in/makeup, free daily blind boxes, gift boxes, signed redemption, farm and work maps. Optional factory assembly practice uses the existing care action without changing rewards.

See [meeting workflows](docs/meeting-workflow.md), [live translation](docs/live-translation.md) and [pet rules](docs/pet-system.md). Companion failures must not block meeting work; LP and rewards have no payments, trading or leaderboard semantics. The 3D companion remains a development preview.

## Install, Launch and Update

Use the assets actually available on [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases), selecting your platform. The current delivery pipeline prepares ad-hoc-signed macOS DMGs and Windows per-user MSIs, alongside portable candidate archives. Trusted publisher signing and Apple notarization are not provided by this community path. Older releases keep their original assets.

For source development, use pinned `pnpm@10.30.3`, Go `1.27.4` and Rust `1.94.0`:

```sh
pnpm install --frozen-lockfile
pnpm native:check
pnpm native:package
pnpm start
```

`pnpm start` launches a verified, already packaged Native candidate; it does not compile source. The launcher selects the highest candidate version for the current platform and activates an existing instance of that build. A different running executable blocks launch. Quit normally before loading a new build. See the [Native guide](apps/native/README.md) for platform behavior, profiles, backups and restore.

Initial local processing requires runtime and model downloads. Update checks run only when requested; downloading and installing an update remain user actions. Never run two clients against the same writable profile.

| Platform | Go target | Packaging argument |
| --- | --- | --- |
| macOS Apple Silicon | `darwin/arm64` | `darwin-arm64` |
| macOS Intel | `darwin/amd64` | `darwin-x64` |
| Windows x64 | `windows/amd64` | `windows-x64` |

Target compilation does not establish native interaction, ASR quality, minimum-OS support or installer acceptance. Remaining requirements are maintained in [release readiness](docs/architecture/release-readiness.md).

## Data and Runtime

Local mode uses SQLite and app-managed Python, ffmpeg and models. The [runtime manifest](packages/desktop-resources/runtime/runtime-manifest.json) pins versions and downloads; the default remains Python 3.11.18 and FunASR `1.3.9+liberty.local2`. `security-candidate` and `funasr-upgrade` are explicit isolated profiles. Schema v13 includes live captions and independent manual meeting drafts. Older binaries require a compatible pre-upgrade snapshot for rollback.

The `processingMode` setting selects local or remote processing; entering `backendUrl` alone does not switch modes. Remote `liberty-meeting` v1 operations cover existing-job queries, retry, deletion and speaker renaming, subject to negotiated capabilities. Remote upload and AI-summary writes are not exposed. API keys and remote tokens use the system credential store; SQLite stores references.

## Development and Documentation

| Path | Responsibility |
| --- | --- |
| `apps/native/` | Native UI, controllers, use cases, domain, infrastructure and host |
| `packages/desktop-resources/` | Shared resources and frozen synthetic compatibility fixtures |
| `packages/shared-types/` | Versioned JSON Schema and generated contracts |
| `python/funasr-runner/` | Runner, validators and platform dependency locks |
| `scripts/`, `config/` | Development, generation, validation and delivery policy |
| `benchmarks/asr/` | Controlled-media and target-device evidence contracts |
| `docs/` | Maintained guides and active work |

Start with the [documentation index](docs/README.md) and [development guide](docs/development.md). Architecture lives in [Native architecture](docs/architecture/native-desktop-architecture.md); [CI/CD rules](docs/architecture/ci-cd.md) define automated gates.

`pnpm native:verify` checks Native and shared Python. `pnpm native:compat` replays frozen compatibility fixtures, and `pnpm native:build:matrix` compiles three targets. Cross-layer changes use `pnpm check`, which also checks the temporary production client. Real dependency scans, device interaction and release verification are separate gates. Documentation-only checks use `pnpm ci:docs`.

## Organization

Liberty is hosted under [whalesky-labs](https://github.com/whalesky-labs). [Repository](https://github.com/whalesky-labs/Liberty) · [Issues](https://github.com/whalesky-labs/Liberty/issues) · [Releases](https://github.com/whalesky-labs/Liberty/releases).

## License

Liberty is licensed under the [MIT License](LICENSE). Dependencies and runtime assets retain their own licenses, notices and distribution requirements.
