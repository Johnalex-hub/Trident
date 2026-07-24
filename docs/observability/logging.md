# Structured Logging & Correlation IDs (issue #294)

Both Trident stacks — the Go REST API (`services/api`) and the Rust services
(`crates/indexer`, `crates/api`) — emit **one JSON object per log line** with an
identical field schema, so a single log aggregator parses both without
stack-specific rules.

## Schema

| Field | Type | Always present | Description |
|-------|------|:--:|-------------|
| `service` | string | ✅ | Emitting service: `trident-api` (Go REST), `trident-grpc-api` (Rust gRPC), `trident-indexer` (Rust indexer) |
| `level` | string | ✅ | Lowercase severity: `debug` \| `info` \| `warn` \| `error` |
| `timestamp` | string | ✅ | RFC 3339 UTC (e.g. `2024-01-01T12:00:00Z`) |
| `message` | string | ✅ | Human-readable log message |
| `request_id` | string | request-scoped | Per-request correlation id (see below) |
| `trace_id` | string | request-scoped | Distributed-trace id (see below) |
| `target` | string | Rust only | Rust module path that emitted the event |
| _\<structured fields\>_ | any | — | Additional key/value context (e.g. `status`, `latency_ms`, `method`, `path`) |

Example (Go REST API request line):

```json
{"service":"trident-api","level":"info","timestamp":"2024-01-01T12:00:00Z","message":"request","method":"GET","path":"/v1/events","status":200,"latency_ms":4,"request_id":"a1b2c3d4e5f6a7b8","trace_id":"0af7651916cd43dd8448eb211c80319c"}
```

Example (Rust indexer line within a request-scoped span):

```json
{"service":"trident-indexer","level":"info","timestamp":"2024-01-01T12:00:00Z","target":"trident_indexer::streamer","message":"handling request","request_id":"a1b2c3d4e5f6a7b8","trace_id":"0af7651916cd43dd8448eb211c80319c"}
```

## Correlation IDs

### `request_id`

- **Go API** (`services/api/middleware`): the `RequestID` middleware reuses an
  inbound `X-Request-ID` header if present, otherwise generates a random
  16-hex-char id. It is stored on the request context, echoed back in the
  `X-Request-ID` response header, and baked into a request-scoped `slog.Logger`
  so **every** log line emitted while handling the request carries it. This is
  the request-id mechanism from #226.
- **Rust**: any log emitted inside a span carrying a `request_id` field inherits
  it automatically (the shared JSON layer merges span fields onto every event).

### `trace_id`

- Derived from the inbound **W3C `traceparent`** header
  (`version-traceid-parentid-flags`); the 32-hex-char trace-id is extracted and
  attached to the context / span. This is forward-compatible with the
  OpenTelemetry integration in #290: once OTel sets `traceparent`, the same
  field flows through with zero further changes.
- When no valid `traceparent` is present, `trace_id` is emitted as an empty
  string so the field is always present for request-scoped lines.

## Implementation

- **Go**: `middleware.InitLogger(service)` installs a JSON `slog.Handler` that
  renames `time`→`timestamp` and `msg`→`message` and pins a `service` base
  attribute. `middleware.RequestID` attaches `request_id` + `trace_id` and a
  request-scoped logger; `middleware.Logging` and handlers log through
  `middleware.Logger(ctx)`. The middleware chain is
  `RequestID(Logging(mux))` so ids are set before anything logs.
- **Rust**: `trident_common::logging::init(service)` installs a custom
  `tracing` layer (`trident_common::logging::JsonLayer`) that serialises each
  event to the schema above and merges span fields (root→leaf) onto every line,
  so request/trace ids on an enclosing span appear on all nested logs. Both
  `crates/indexer` and `crates/api` call it at startup.

## Tests

- Go: `services/api/middleware/logging_test.go` runs a request through the
  middleware chain with a `traceparent` header and asserts every emitted JSON
  line carries the matching `request_id`, `trace_id`, `service`, and
  `timestamp`.
- Rust: `crates/common/src/logging.rs` tests assert the schema fields are
  present and that a log emitted inside a span carrying `request_id`/`trace_id`
  includes both on the line.
