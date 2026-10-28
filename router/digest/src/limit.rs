//! Bandwidth limits that HAProxy cannot apply because the bytes never pass it.
//!
//! HAProxy limits each route (host and country, the bwlim key) with a shared
//! bwlim-out filter. The digested bodies leave HAProxy as a few bytes, so the
//! proxy limits its reads from the app instead: one shared bucket per route with
//! the same limit, refilled at the limit minus what HAProxy itself forwards on
//! that route, so that the route as a whole stays within its limit.

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, Instant};

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

/// HAProxy's bwlim forwards at least this much at a time.
const MIN_GRANT: usize = 1500;

pub struct Bucket {
    /// Bytes per second.
    limit: f64,
    /// HAProxy's own bytes_out_rate on the route, as f64 bits.
    haproxy_rate: AtomicU64,
    /// Tokens (may be negative after a charge) and when they were counted.
    state: Mutex<(f64, Instant)>,
}

impl Bucket {
    pub fn new(limit: f64) -> Self {
        Bucket {
            limit,
            haproxy_rate: AtomicU64::new(0f64.to_bits()),
            state: Mutex::new((limit, Instant::now())),
        }
    }

    /// The refill rate. A tenth of the limit stays for the digested bodies even
    /// when HAProxy's own traffic fills the route, as a shared bwlim would let them
    /// through.
    fn refill_rate(&self) -> f64 {
        let haproxy = f64::from_bits(self.haproxy_rate.load(Ordering::Relaxed));
        (self.limit - haproxy).max(self.limit / 10.0)
    }

    /// Takes up to max bytes, or says how long to wait for the minimum grant. The
    /// bucket holds up to one second of the limit, like bwlim's one-second period.
    fn take(&self, max: usize, now: Instant) -> Result<usize, Duration> {
        let rate = self.refill_rate();
        let mut s = self.state.lock().unwrap();
        s.0 = (s.0 + rate * now.saturating_duration_since(s.1).as_secs_f64()).min(self.limit);
        s.1 = now;
        let want = max.min(MIN_GRANT) as f64;
        if s.0 >= want {
            let n = (s.0 as usize).min(max);
            s.0 -= n as f64;
            Ok(n)
        } else {
            Err(Duration::from_secs_f64((want - s.0) / rate).max(Duration::from_micros(500)))
        }
    }

    /// Charges bytes read without a grant (negative to refund an unused grant).
    pub fn charge(&self, n: f64) {
        self.state.lock().unwrap().0 -= n;
    }

    pub async fn acquire(&self, max: usize) -> usize {
        loop {
            match self.take(max, Instant::now()) {
                Ok(n) => return n,
                Err(wait) => tokio::time::sleep(wait).await,
            }
        }
    }
}

fn buckets() -> &'static Mutex<HashMap<String, Arc<Bucket>>> {
    static BUCKETS: OnceLock<Mutex<HashMap<String, Arc<Bucket>>>> = OnceLock::new();
    BUCKETS.get_or_init(Default::default)
}

/// The bucket of route, created with limit (bytes per second) on first use.
pub fn bucket(route: &str, limit: f64) -> Arc<Bucket> {
    let mut all = buckets().lock().unwrap();
    match all.get(route) {
        Some(b) if b.limit == limit => b.clone(),
        _ => {
            let b = Arc::new(Bucket::new(limit));
            all.insert(route.to_string(), b.clone());
            b
        }
    }
}

/// Parses `show table bandwidth` into (route, bytes_out_rate) pairs.
fn parse_table(out: &[u8]) -> Vec<(String, f64)> {
    let mut rates = Vec::new();
    for line in out.split(|&c| c == b'\n') {
        let line = String::from_utf8_lossy(line);
        let key = line
            .split_once(" key=")
            .and_then(|(_, r)| r.split(' ').next());
        let rate = line
            .split_once(" bytes_out_rate(")
            .and_then(|(_, r)| r.split_once(")="))
            .and_then(|(_, r)| r.split(|c: char| !c.is_ascii_digit()).next())
            .and_then(|v| v.parse::<f64>().ok());
        if let (Some(key), Some(rate)) = (key, rate) {
            rates.push((key.to_string(), rate));
        }
    }
    rates
}

/// Keeps every bucket's view of HAProxy's own rate on its route fresh, reading
/// the bwlim table through the HAProxy runtime API every 100 ms.
pub async fn poll_haproxy(address: String) {
    loop {
        tokio::time::sleep(Duration::from_millis(100)).await;
        let Ok(Ok(mut s)) =
            tokio::time::timeout(Duration::from_secs(1), TcpStream::connect(&address)).await
        else {
            continue;
        };
        let mut out = Vec::new();
        let ok = s.write_all(b"show table bandwidth\n").await.is_ok()
            && matches!(
                tokio::time::timeout(Duration::from_secs(1), s.read_to_end(&mut out)).await,
                Ok(Ok(_))
            );
        if !ok {
            continue;
        }
        let all = buckets().lock().unwrap();
        for (route, rate) in parse_table(&out) {
            if let Some(b) = all.get(&route) {
                b.haproxy_rate.store(rate.to_bits(), Ordering::Relaxed);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn table() {
        let out = b"# table: bandwidth, type: string, size:64, used:2\n\
            0x75ea24087fd8: key=app/AU use=60 exp=0 shard=0 bytes_in_rate(1000)=10862 bytes_out_rate(1000)=1972176\n\
            0x5e8647154148: key=app1/JP use=0 exp=0 shard=0 bytes_in_rate(1000)=0 bytes_out_rate(1000)=0\n\n";
        assert_eq!(
            parse_table(out),
            vec![
                ("app/AU".to_string(), 1972176.0),
                ("app1/JP".to_string(), 0.0)
            ]
        );
    }

    #[test]
    fn bucket_rate() {
        let start = Instant::now();
        let b = Bucket::new(1_000_000.0);
        // One second of burst, then the limit.
        assert_eq!(b.take(10_000_000, start), Ok(1_000_000));
        assert!(b.take(10_000_000, start).is_err());
        assert_eq!(
            b.take(10_000_000, start + Duration::from_millis(500)),
            Ok(500_000)
        );
        // HAProxy's own traffic slows the refill, down to a tenth of the limit.
        b.haproxy_rate
            .store(800_000f64.to_bits(), Ordering::Relaxed);
        assert_eq!(
            b.take(10_000_000, start + Duration::from_millis(1500)),
            Ok(200_000)
        );
        b.haproxy_rate
            .store(5_000_000f64.to_bits(), Ordering::Relaxed);
        assert_eq!(
            b.take(10_000_000, start + Duration::from_millis(2500)),
            Ok(100_000)
        );
        // Bytes read with the head put the bucket in debt.
        b.charge(50_000.0);
        let wait = b
            .take(10_000_000, start + Duration::from_millis(2500))
            .unwrap_err();
        assert!(
            wait > Duration::from_millis(500) && wait < Duration::from_millis(600),
            "{wait:?}"
        );
    }
}
