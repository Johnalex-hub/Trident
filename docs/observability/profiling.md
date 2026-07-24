# Profiling Endpoints (issue #299)

Opt-in, access-gated profiling for diagnosing latency spikes and task/memory
leaks under load. **Everything here is off by default and must never be exposed
publicly.**

## Go API — `net/http/pprof`

`services/api/internal/profiling` mounts the standard `net/http/pprof`
endpoints, with two safety properties:

1. **Off by default.** Nothing is started unless `PPROF_ENABLED=true` (exact
   match). Any other value — unset, `1`, `yes`, `false` — leaves profiling off
   and opens no listener.
2. **Never on the public port.** pprof is served from a **separate**
   `http.Server`, never mounted on the public API mux. It binds to
   `127.0.0.1:6060` by default, so it is unreachable from outside the host.

### Enabling

```bash
PPROF_ENABLED=true PORT=3000 ./api
# pprof now on http://127.0.0.1:6060/debug/pprof/ (loopback only)
```

To reach it from another host, do **not** bind it to `0.0.0.0`. Instead keep the
loopback bind and tunnel in:

```bash
ssh -L 6060:127.0.0.1:6060 user@host
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30
```

`PPROF_ADDR` can point it at a dedicated internal interface (e.g. a private
management network) if loopback isn't sufficient — but it must remain outside
the public ingress. The public ingress only routes to the API `PORT`; the pprof
port is never added to it.

### Endpoints

`/debug/pprof/` (index), `/cmdline`, `/profile` (CPU), `/symbol`, `/trace`.

## Rust indexer — tokio-console

`crates/indexer` integrates [`tokio-console`](https://github.com/tokio-rs/console)
for async task profiling, gated on **two** levels:

1. **Compile-time:** behind the non-default `tokio-console` cargo feature, so
   production release builds don't include it at all.
2. **Runtime:** even when compiled in, it stays off unless
   `TOKIO_CONSOLE_ENABLED=true`.

The console subscriber binds to `127.0.0.1:6669` by default (configurable via
`TOKIO_CONSOLE_BIND`), so it is not publicly reachable.

### Enabling

Full task instrumentation requires building `tokio` with the `tokio_unstable`
cfg:

```bash
RUSTFLAGS="--cfg tokio_unstable" \
  cargo build -p trident-indexer --features tokio-console

TOKIO_CONSOLE_ENABLED=true ./trident-indexer
# In another terminal:
tokio-console   # connects to 127.0.0.1:6669
```

Without `TOKIO_CONSOLE_ENABLED=true`, the indexer logs normally and starts no
console server, regardless of how it was built.

## Security summary

| Service | Mechanism | Default | Gate | Bind |
|---------|-----------|:--:|------|------|
| Go API | `net/http/pprof` | off | `PPROF_ENABLED=true` | `127.0.0.1:6060` (loopback) |
| Rust indexer | tokio-console | off | `tokio-console` feature **and** `TOKIO_CONSOLE_ENABLED=true` | `127.0.0.1:6669` (loopback) |

Neither endpoint is ever added to the public API mux/ingress. Verified by
`services/api/internal/profiling/profiling_test.go`:

- `Start()` returns no server (opens no port) when `PPROF_ENABLED` is unset.
- Only the exact value `true` enables it.
- The default bind address is loopback.
- The pprof mux serves `/debug/pprof/` but 404s any non-pprof path, so it can't
  be conflated with the API surface.
