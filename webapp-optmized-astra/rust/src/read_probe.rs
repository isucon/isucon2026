use std::sync::LazyLock;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Instant, SystemTime, UNIX_EPOCH};

static ENABLED: LazyLock<bool> =
    LazyLock::new(|| std::env::var("ISUREAL_READ_TIMING").as_deref() == Ok("1"));
static REQUESTS: AtomicU64 = AtomicU64::new(0);

/// Opt-in, one-in-eight memory request timings. Contains no request identifiers.
#[derive(Clone)]
pub struct ReadProbe {
    started: Instant,
    last: Instant,
    route: &'static str,
    stages: Vec<(&'static str, u64)>,
}

impl ReadProbe {
    pub fn sample(path: &str) -> Option<Self> {
        if !*ENABLED {
            return None;
        }
        let route = if path == "/api/memories" {
            "month"
        } else if path.starts_with("/api/memories/") {
            "day"
        } else {
            return None;
        };
        if !REQUESTS.fetch_add(1, Ordering::Relaxed).is_multiple_of(8) {
            return None;
        }
        let started = Instant::now();
        Some(Self {
            started,
            last: started,
            route,
            stages: Vec::with_capacity(10),
        })
    }

    pub fn mark(probe: &mut Option<Self>, stage: &'static str) {
        if let Some(probe) = probe {
            let now = Instant::now();
            probe
                .stages
                .push((stage, now.duration_since(probe.last).as_micros() as u64));
            probe.last = now;
        }
    }

    pub fn finish(probe: Option<Self>, posts: usize) {
        if let Some(probe) = probe {
            let stages: serde_json::Map<String, serde_json::Value> = probe
                .stages
                .into_iter()
                .map(|(name, us)| (name.to_owned(), us.into()))
                .collect();
            eprintln!(
                "{}",
                serde_json::json!({
                    "event": "memory_read_timing", "route": probe.route,
                    "at_ms": SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_millis(),
                    "total_us": probe.started.elapsed().as_micros(), "posts": posts,
                    "stages_us": stages,
                })
            );
        }
    }
}
