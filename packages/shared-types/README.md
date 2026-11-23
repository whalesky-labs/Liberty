# Liberty Shared Contracts

Versioned JSON Schema files under `schemas/` are the source of truth for the
retained Rust/TypeScript client, Python Runner validation and native Go candidate.
The Go client does not replace these schemas with a second protocol definition.

After a schema change, run from the repository root:

```sh
pnpm contracts:generate
pnpm native:contracts:generate
pnpm contracts:check
pnpm native:contracts:check
```

The first generator maintains the Rust and TypeScript outputs; the native
generator embeds the schemas under `apps/native/internal/contracts/`. Go domain
DTOs and adapters must also be checked against changed fields: schema generation
does not automatically update every consumer. Review local, remote, persisted
snapshot and export paths together.

Do not hand-edit generated files under `src/generated/` or other generated
destinations. See [native architecture](../../docs/architecture/native-desktop-architecture.md)
and the [development guide](../../docs/development.md) for verification boundaries.
