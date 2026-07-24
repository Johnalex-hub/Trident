//! Shared structured-logging setup for all Trident Rust services.
//!
//! Emits one JSON object per log line with a schema that matches the Go API's
//! `slog` output, so a log aggregator can parse both stacks identically:
//!
//! ```json
//! {"service":"trident-indexer","level":"info","timestamp":"2024-01-01T00:00:00Z",
//!  "target":"trident_indexer","message":"...","request_id":"...","trace_id":"..."}
//! ```
//!
//! `request_id` / `trace_id` are pulled from the surrounding `tracing` span, so
//! any code that runs inside a span carrying those fields (e.g. a request-scoped
//! span seeded from the W3C `traceparent` header / request-id middleware) gets
//! them attached to every log line automatically.
//!
//! See `docs/observability/logging.md` for the full schema.

use std::io::Write;
use std::sync::{Arc, Mutex};

use tracing::field::{Field, Visit};
use tracing::{Event, Subscriber};
use tracing_subscriber::layer::{Context, SubscriberExt};
use tracing_subscriber::registry::LookupSpan;
use tracing_subscriber::util::SubscriberInitExt;
use tracing_subscriber::{EnvFilter, Layer};

/// Install the shared JSON logging subscriber as the global default.
///
/// `service` is emitted as the `service` field on every log line. Respects
/// `RUST_LOG` for level filtering, defaulting to `info`.
pub fn init(service: &'static str) {
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    let layer = JsonLayer::new(service, std::io::stdout);
    tracing_subscriber::registry()
        .with(filter)
        .with(layer)
        .init();
}

/// A `tracing` layer that serialises each event to a single JSON line using the
/// shared Trident log schema, writing through the supplied `MakeWriter`.
pub struct JsonLayer<W> {
    service: &'static str,
    make_writer: W,
}

impl<W> JsonLayer<W> {
    pub fn new(service: &'static str, make_writer: W) -> Self {
        Self {
            service,
            make_writer,
        }
    }
}

/// Span fields captured at span creation, stored in the span's extensions so
/// they can be merged onto every event emitted within that span.
struct SpanFields(serde_json::Map<String, serde_json::Value>);

/// Visits `tracing` field values, collecting them into a JSON map and pulling
/// out the special `message` field.
#[derive(Default)]
struct FieldVisitor {
    fields: serde_json::Map<String, serde_json::Value>,
    message: Option<String>,
}

impl Visit for FieldVisitor {
    fn record_str(&mut self, field: &Field, value: &str) {
        if field.name() == "message" {
            self.message = Some(value.to_owned());
        } else {
            self.fields
                .insert(field.name().to_owned(), value.to_owned().into());
        }
    }

    fn record_i64(&mut self, field: &Field, value: i64) {
        self.fields.insert(field.name().to_owned(), value.into());
    }

    fn record_u64(&mut self, field: &Field, value: u64) {
        self.fields.insert(field.name().to_owned(), value.into());
    }

    fn record_bool(&mut self, field: &Field, value: bool) {
        self.fields.insert(field.name().to_owned(), value.into());
    }

    fn record_debug(&mut self, field: &Field, value: &dyn std::fmt::Debug) {
        let rendered = format!("{value:?}");
        if field.name() == "message" {
            self.message = Some(rendered);
        } else {
            self.fields.insert(field.name().to_owned(), rendered.into());
        }
    }
}

impl<S, W> Layer<S> for JsonLayer<W>
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    W: for<'a> tracing_subscriber::fmt::MakeWriter<'a> + 'static,
{
    fn on_new_span(
        &self,
        attrs: &tracing::span::Attributes<'_>,
        id: &tracing::span::Id,
        ctx: Context<'_, S>,
    ) {
        let Some(span) = ctx.span(id) else { return };
        let mut visitor = FieldVisitor::default();
        attrs.record(&mut visitor);
        span.extensions_mut().insert(SpanFields(visitor.fields));
    }

    fn on_event(&self, event: &Event<'_>, ctx: Context<'_, S>) {
        let meta = event.metadata();

        let mut obj = serde_json::Map::new();
        obj.insert("service".into(), self.service.into());
        obj.insert("level".into(), meta.level().as_str().to_lowercase().into());
        obj.insert("timestamp".into(), now_rfc3339().into());
        obj.insert("target".into(), meta.target().into());

        // Merge span fields root -> leaf so request_id / trace_id from an outer
        // request span are present on every log line within it.
        if let Some(scope) = ctx.event_scope(event) {
            for span in scope.from_root() {
                if let Some(fields) = span.extensions().get::<SpanFields>() {
                    for (k, v) in &fields.0 {
                        obj.insert(k.clone(), v.clone());
                    }
                }
            }
        }

        let mut visitor = FieldVisitor::default();
        event.record(&mut visitor);
        if let Some(message) = visitor.message {
            obj.insert("message".into(), message.into());
        }
        // Event fields win over span fields on key collisions.
        for (k, v) in visitor.fields {
            obj.insert(k, v);
        }

        let mut line = serde_json::to_string(&obj).unwrap_or_default();
        line.push('\n');
        let mut writer = self.make_writer.make_writer();
        let _ = writer.write_all(line.as_bytes());
    }
}

/// RFC3339 UTC timestamp with second precision (matches the Go side's format).
fn now_rfc3339() -> String {
    chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Secs, true)
}

/// A `MakeWriter` that appends to a shared in-memory buffer. Test-only helper
/// used to assert on emitted log lines.
#[derive(Clone)]
pub struct SharedBuffer(Arc<Mutex<Vec<u8>>>);

impl SharedBuffer {
    pub fn new() -> Self {
        Self(Arc::new(Mutex::new(Vec::new())))
    }

    /// Returns the buffer's current contents as a UTF-8 string.
    pub fn contents(&self) -> String {
        String::from_utf8_lossy(&self.0.lock().expect("lock poisoned")).into_owned()
    }
}

impl Default for SharedBuffer {
    fn default() -> Self {
        Self::new()
    }
}

/// Write guard handed out by `SharedBuffer`'s `MakeWriter` impl.
pub struct SharedBufferGuard(Arc<Mutex<Vec<u8>>>);

impl Write for SharedBufferGuard {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.lock().expect("lock poisoned").extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

impl<'a> tracing_subscriber::fmt::MakeWriter<'a> for SharedBuffer {
    type Writer = SharedBufferGuard;

    fn make_writer(&'a self) -> Self::Writer {
        SharedBufferGuard(self.0.clone())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_line_has_shared_schema_fields() {
        let buf = SharedBuffer::new();
        let subscriber =
            tracing_subscriber::registry().with(JsonLayer::new("trident-test", buf.clone()));

        tracing::subscriber::with_default(subscriber, || {
            tracing::info!(answer = 42, "hello");
        });

        let line = buf.contents();
        let v: serde_json::Value = serde_json::from_str(line.trim()).expect("valid JSON line");
        assert_eq!(v["service"], "trident-test");
        assert_eq!(v["level"], "info");
        assert_eq!(v["message"], "hello");
        assert_eq!(v["answer"], 42);
        assert!(v["timestamp"].is_string());
    }

    #[test]
    fn request_scoped_log_carries_correlation_ids() {
        let buf = SharedBuffer::new();
        let subscriber =
            tracing_subscriber::registry().with(JsonLayer::new("trident-test", buf.clone()));

        tracing::subscriber::with_default(subscriber, || {
            let span =
                tracing::info_span!("request", request_id = "req-123", trace_id = "trace-abc");
            let _guard = span.enter();
            tracing::info!("handling request");
        });

        let line = buf.contents();
        let v: serde_json::Value = serde_json::from_str(line.trim()).expect("valid JSON line");
        // Correlation ids from the surrounding span appear on the log line.
        assert_eq!(v["request_id"], "req-123");
        assert_eq!(v["trace_id"], "trace-abc");
        assert_eq!(v["message"], "handling request");
    }
}
