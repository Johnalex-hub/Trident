use tokio_util::sync::CancellationToken;

mod config;
mod db;
mod parser;
mod redis_stream;
mod rpc;
mod streamer;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    init_tracing();

    tracing::info!("Trident indexer starting");

    let cfg = config::Config::from_env()?;

    let db_pool = sqlx::PgPool::connect(&cfg.database_url).await?;
    tracing::info!("Database connected");

    let redis_client = redis::Client::open(cfg.redis_url.as_str())?;
    let redis_conn = redis_client.get_multiplexed_async_connection().await?;
    tracing::info!("Redis connected");

    let shutdown = CancellationToken::new();

    // Spawn signal watcher — cancels the token on SIGTERM or SIGINT.
    let shutdown_trigger = shutdown.clone();
    tokio::spawn(async move {
        #[cfg(unix)]
        {
            use tokio::signal::unix::{signal, SignalKind};
            let mut sigterm =
                signal(SignalKind::terminate()).expect("failed to register SIGTERM handler");
            tokio::select! {
                _ = tokio::signal::ctrl_c() => {
                    tracing::info!("Received SIGINT, initiating graceful shutdown");
                }
                _ = sigterm.recv() => {
                    tracing::info!("Received SIGTERM, initiating graceful shutdown");
                }
            }
        }
        #[cfg(not(unix))]
        {
            let _ = tokio::signal::ctrl_c().await;
            tracing::info!("Received SIGINT, initiating graceful shutdown");
        }

        shutdown_trigger.cancel();
    });

    let mut s = streamer::Streamer::new(cfg, db_pool, redis_conn);
    s.run(shutdown).await?;

    tracing::info!("Trident indexer stopped");
    Ok(())
}

/// Initialise tracing/logging, optionally enabling the tokio-console async
/// profiler.
///
/// tokio-console is opt-in on two levels so it can never be reached in a normal
/// deployment: it is compiled out unless the `tokio-console` cargo feature is
/// built, and even then it stays off unless `TOKIO_CONSOLE_ENABLED=true`. The
/// console server binds to `127.0.0.1:6669` by default (see console-subscriber
/// docs / `TOKIO_CONSOLE_BIND`), so it is not publicly reachable.
fn init_tracing() {
    #[cfg(feature = "tokio-console")]
    if std::env::var("TOKIO_CONSOLE_ENABLED").as_deref() == Ok("true") {
        use tracing_subscriber::prelude::*;
        let console_layer = console_subscriber::spawn();
        // Keep the shared JSON log schema alongside the console profiler.
        let json_layer =
            trident_common::logging::JsonLayer::new("trident-indexer", std::io::stdout)
                .with_filter(default_filter());
        tracing_subscriber::registry()
            .with(console_layer)
            .with(json_layer)
            .init();
        tracing::warn!(
            "tokio-console ENABLED on 127.0.0.1:6669 — internal/debug use only, never expose publicly"
        );
        return;
    }

    trident_common::logging::init("trident-indexer");
}

#[cfg(feature = "tokio-console")]
fn default_filter() -> tracing_subscriber::EnvFilter {
    tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"))
}
