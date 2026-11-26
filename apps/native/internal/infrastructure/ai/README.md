# AI completion boundary

[原生客户端说明](../../../README.md) · [文档总览](../../../../../docs/README.md)

`Client` implements the OpenAI-compatible HTTP transport. The application owns
credentials, durable summary runs, retries and publication. Response validation
is in `domain.ParseSummaryStructured`; views do not interpret provider JSON.

The completion envelope is enforced by
[`domain/ai_response_wire.go`](../../domain/ai_response_wire.go) and its
[`wire regressions`](../../domain/ai_response_wire_test.go):

- `choices`, `finish_reason`, `message`, `content` and content-part `text` are
  case-sensitive. Differently cased fields are unknown extensions, not aliases.
- Duplicate known fields are invalid, including escaped spellings of the same
  key and duplicates whose first value is `null`. Unknown extensions remain
  ignored, including repeated unknown fields.
- Every choice and content part must have a valid shape before the first choice
  is selected. Optional fields may be missing or null; a choice or part itself
  must be an object. A successful first choice requires `finish_reason: "stop"`
  after trimming and a non-null string or text-parts content value.
- The structured JSON inside the content follows JSON-value semantics: duplicate
  object keys keep the last value. It must be one object with the nine required
  summary fields. The response size limit remains 4 MiB.

`SummaryService` validates a response before saving a completed chunk or
publishing minutes. The repository checks the same parser when saving chunks
and completing a run. An invalid response produces the stable
`ai_response_invalid` failure code; it does not publish an active summary,
completed chunk or completion event. A user can explicitly retry the failed run.
Previously saved chunks are revalidated on resume; malformed historical chunks
are not silently accepted or rewritten.

`summary_persistence_test.go` uses a loopback HTTP provider, the production
`Client` and `SummaryService`, temporary SQLite, and the production result reader.
It checks failure and explicit retry across connection/service restarts. Domain
wire tests cover exact names, duplicates, nulls, parts and unused extra choices.
These tests use synthetic content and do not call an external AI service.


Live captions use a separate local translation process and do not use this HTTP
client, model configuration or credentials. See [live translation](../../../../../docs/live-translation.md).
