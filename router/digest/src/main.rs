//! isureal-digest answers the benchmark's image and export downloads with
//! digests so that the Router does not forward those bytes twice.
//!
//! HAProxy sends here the GET requests to /api/images/* and /api/me/export that
//! carry X-Benchmark-Request-Id, adding X-Isureal-Upstream (the app's address)
//! and the route's bandwidth limit (X-Isureal-Route, X-Isureal-Limit). The proxy
//! removes those headers, reads the app's whole response and answers 200 with
//! X-Body-Xxh3 (the XXH3-64 of the body, seeded by the request id) and
//! X-Body-Length instead of the body. In an export ZIP every entry except the
//! JSON files is replaced by its digest (zip::MARKER). Other statuses, and
//! bodies with a Content-Encoding (which the benchmark decodes before comparing),
//! pass through unchanged.
//!
//! Configuration comes from the Router's environment:
//! ROUTER_DIGEST_ADDRESS (where to listen, default 127.0.0.1:18010) and
//! ROUTER_HAPROXY_RUNTIME_ADDRESS (the HAProxy runtime API, default
//! 127.0.0.1:18001).

mod body;
mod limit;
mod zip;

use std::collections::HashMap;
use std::io;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::Duration;

use tokio::io::AsyncWriteExt;
use tokio::net::{TcpListener, TcpStream};
use xxhash_rust::xxh3::Xxh3;

use body::{invalid, read_body, read_exact_to, Buf, Framing, Sink};
use limit::Bucket;

const UPSTREAM_BUFFER: usize = 256 << 10;
const DOWNSTREAM_BUFFER: usize = 64 << 10;
/// HAProxy gives up on a server after 60 s (timeout server).
const TIMEOUT: Duration = Duration::from_secs(60);

static REQUESTS: AtomicU64 = AtomicU64::new(0);
static DIGESTED_BYTES: AtomicU64 = AtomicU64::new(0);

/// Idle keep-alive connections to one app.
struct Upstream {
    address: String,
    idle: Mutex<Vec<TcpStream>>,
}

impl Upstream {
    fn take(&self) -> Option<TcpStream> {
        self.idle.lock().unwrap().pop()
    }

    fn put(&self, s: TcpStream) {
        let mut idle = self.idle.lock().unwrap();
        if idle.len() < 4096 {
            idle.push(s);
        }
    }

    async fn connect(&self) -> io::Result<TcpStream> {
        let s = TcpStream::connect(&self.address).await?;
        s.set_nodelay(true)?;
        Ok(s)
    }
}

fn upstream(address: &str) -> Arc<Upstream> {
    static UPSTREAMS: OnceLock<Mutex<HashMap<String, Arc<Upstream>>>> = OnceLock::new();
    let mut all = UPSTREAMS.get_or_init(Default::default).lock().unwrap();
    all.entry(address.to_string())
        .or_insert_with(|| {
            Arc::new(Upstream {
                address: address.to_string(),
                idle: Mutex::new(Vec::new()),
            })
        })
        .clone()
}

fn is_hop(name: &str) -> bool {
    [
        "connection",
        "keep-alive",
        "proxy-connection",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
    ]
    .iter()
    .any(|h| name.eq_ignore_ascii_case(h))
}

fn parse_header<T: std::str::FromStr>(value: &[u8]) -> Option<T> {
    std::str::from_utf8(value).ok()?.trim().parse().ok()
}

/// What HAProxy asked for besides the request itself.
struct Request {
    upstream: Option<String>,
    seed: Option<u64>,
    route: Option<String>,
    limit: Option<f64>,
    export: bool,
    head_only: bool,
    close: bool,
}

/// Buffers reused across the requests of one downstream connection.
struct Conn {
    request: Vec<u8>,
    upstream: Buf,
    head: Vec<u8>,
    body: Vec<u8>,
    zip: Vec<u8>,
    out: Vec<u8>,
}

/// Turns the request head into the request for the app (in c.request) without
/// the headers meant for this proxy.
fn parse_request(head: &[u8], c: &mut Conn) -> io::Result<(Request, u64)> {
    let mut headers = [httparse::EMPTY_HEADER; 64];
    let mut r = httparse::Request::new(&mut headers);
    if !r
        .parse(head)
        .map_err(|_| invalid("request head"))?
        .is_complete()
    {
        return Err(invalid("request head"));
    }
    let method = r.method.unwrap_or("GET");
    let path = r.path.unwrap_or("/");
    let mut req = Request {
        upstream: None,
        seed: None,
        route: None,
        limit: None,
        export: path == "/api/me/export" || path.starts_with("/api/me/export?"),
        head_only: method == "HEAD",
        close: r.version == Some(0),
    };
    let mut length = 0;
    c.request.clear();
    c.request.extend_from_slice(method.as_bytes());
    c.request.push(b' ');
    c.request.extend_from_slice(path.as_bytes());
    c.request.extend_from_slice(b" HTTP/1.1\r\n");
    for h in r.headers.iter() {
        let name = h.name;
        if name.eq_ignore_ascii_case("x-isureal-upstream") {
            req.upstream = std::str::from_utf8(h.value)
                .ok()
                .map(|v| v.trim().to_string());
            continue;
        }
        if name.eq_ignore_ascii_case("x-isureal-route") {
            req.route = std::str::from_utf8(h.value)
                .ok()
                .map(|v| v.trim().to_string());
            continue;
        }
        if name.eq_ignore_ascii_case("x-isureal-limit") {
            req.limit = parse_header::<f64>(h.value).filter(|&v| v > 0.0);
            continue;
        }
        if name.eq_ignore_ascii_case("x-benchmark-request-id") {
            req.seed = parse_header(h.value);
            continue;
        }
        if name.eq_ignore_ascii_case("content-length") {
            length = parse_header(h.value).ok_or_else(|| invalid("content-length"))?;
        }
        if name.eq_ignore_ascii_case("transfer-encoding") {
            return Err(invalid("chunked request"));
        }
        if name.eq_ignore_ascii_case("connection") {
            let v = h.value.to_ascii_lowercase();
            req.close = memchr::memmem::find(&v, b"close").is_some()
                || (req.close && memchr::memmem::find(&v, b"keep-alive").is_none());
            continue;
        }
        if is_hop(name) {
            continue;
        }
        c.request.extend_from_slice(name.as_bytes());
        c.request.extend_from_slice(b": ");
        c.request.extend_from_slice(h.value);
        c.request.extend_from_slice(b"\r\n");
    }
    c.request.extend_from_slice(b"\r\n");
    Ok((req, length))
}

/// Sends c.request to the app and leaves the response for HAProxy in c.out.
async fn exchange(
    up: &Upstream,
    req: &Request,
    limit: Option<&Arc<Bucket>>,
    c: &mut Conn,
) -> io::Result<()> {
    let mut stale = 0;
    loop {
        // A pooled connection may have been closed by the app; retry the
        // (idempotent) request on another one.
        let (mut conn, pooled) = match up.take() {
            Some(conn) if stale < 4 => (conn, true),
            _ => (up.connect().await?, false),
        };
        c.upstream.clear();
        if let Err(e) = conn.write_all(&c.request).await {
            if pooled {
                stale += 1;
                continue;
            }
            return Err(e);
        }
        let head_len = match c.upstream.read_head(&mut conn).await {
            Ok(Some(n)) => n,
            Ok(None) | Err(_) if pooled => {
                stale += 1;
                continue;
            }
            Ok(None) => return Err(body::eof()),
            Err(e) => return Err(e),
        };
        c.head.clear();
        c.head.extend_from_slice(&c.upstream.avail()[..head_len]);
        c.upstream.consume(head_len);

        let code;
        let mut framing = Framing::Close;
        let mut encoded = false;
        let mut reusable;
        c.out.clear();
        {
            let mut headers = [httparse::EMPTY_HEADER; 64];
            let mut resp = httparse::Response::new(&mut headers);
            if !resp
                .parse(&c.head)
                .map_err(|_| invalid("response head"))?
                .is_complete()
            {
                return Err(invalid("response head"));
            }
            code = resp.code.unwrap_or(502);
            reusable = resp.version == Some(1);
            c.out.extend_from_slice(
                format!("HTTP/1.1 {} {}\r\n", code, resp.reason.unwrap_or("")).as_bytes(),
            );
            for h in resp.headers.iter() {
                if h.name.eq_ignore_ascii_case("content-length") {
                    framing = Framing::Length(
                        parse_header(h.value).ok_or_else(|| invalid("content-length"))?,
                    );
                    continue;
                }
                if h.name.eq_ignore_ascii_case("transfer-encoding") {
                    if h.value.eq_ignore_ascii_case(b"chunked") {
                        framing = Framing::Chunked;
                    }
                    continue;
                }
                if h.name.eq_ignore_ascii_case("connection") {
                    if memchr::memmem::find(&h.value.to_ascii_lowercase(), b"close").is_some() {
                        reusable = false;
                    }
                    continue;
                }
                if is_hop(h.name) {
                    continue;
                }
                if h.name.eq_ignore_ascii_case("content-encoding")
                    && !h.value.trim_ascii().eq_ignore_ascii_case(b"identity")
                {
                    encoded = true;
                }
                c.out.extend_from_slice(h.name.as_bytes());
                c.out.extend_from_slice(b": ");
                c.out.extend_from_slice(h.value);
                c.out.extend_from_slice(b"\r\n");
            }
        }
        if req.head_only || code == 204 || code == 304 || (100..200).contains(&code) {
            framing = Framing::Empty;
        }
        if framing == Framing::Close {
            reusable = false;
        }
        // Body bytes read together with the head count against the limit too.
        if let Some(limit) = limit {
            limit.charge(c.upstream.avail().len() as f64);
            c.upstream.limit = Some(limit.clone());
        }

        let digest = code == 200 && !encoded;
        match req.seed {
            Some(seed) if digest && !req.export => {
                let mut hash = Xxh3::with_seed(seed);
                let mut n = 0u64;
                read_body(
                    &mut c.upstream,
                    &mut conn,
                    framing,
                    &mut Sink::Hash(&mut hash, &mut n),
                )
                .await?;
                DIGESTED_BYTES.fetch_add(n, Ordering::Relaxed);
                c.out.extend_from_slice(
                    format!(
                        "X-Body-Xxh3: {:016x}\r\nX-Body-Length: {}\r\nContent-Length: 0\r\n\r\n",
                        hash.digest(),
                        n
                    )
                    .as_bytes(),
                );
            }
            _ => {
                c.body.clear();
                read_body(
                    &mut c.upstream,
                    &mut conn,
                    framing,
                    &mut Sink::Collect(&mut c.body),
                )
                .await?;
                let mut payload: &[u8] = &c.body;
                if let Some(seed) = req.seed.filter(|_| digest) {
                    if zip::rewrite(&c.body, seed, &mut c.zip).is_some() {
                        DIGESTED_BYTES.fetch_add(c.body.len() as u64, Ordering::Relaxed);
                        c.out.extend_from_slice(
                            format!("X-Body-Length: {}\r\n", c.body.len()).as_bytes(),
                        );
                        payload = &c.zip;
                    }
                }
                c.out.extend_from_slice(
                    format!("Content-Length: {}\r\n\r\n", payload.len()).as_bytes(),
                );
                c.out.extend_from_slice(payload);
            }
        }
        c.upstream.limit = None;
        if reusable {
            up.put(conn);
        }
        return Ok(());
    }
}

async fn serve(mut down: TcpStream) -> io::Result<()> {
    down.set_nodelay(true)?;
    let mut buf = Buf::new(DOWNSTREAM_BUFFER);
    let mut c = Conn {
        request: Vec::with_capacity(4096),
        upstream: Buf::new(UPSTREAM_BUFFER),
        head: Vec::with_capacity(4096),
        body: Vec::new(),
        zip: Vec::new(),
        out: Vec::with_capacity(4096),
    };
    loop {
        let Some(head_len) = buf.read_head(&mut down).await? else {
            return Ok(());
        };
        let (req, length) = parse_request(&buf.avail()[..head_len], &mut c)?;
        buf.consume(head_len);
        if length > 0 {
            read_exact_to(
                &mut buf,
                &mut down,
                length,
                &mut Sink::Collect(&mut c.request),
            )
            .await?;
        }
        REQUESTS.fetch_add(1, Ordering::Relaxed);
        let limit = req
            .route
            .as_deref()
            .zip(req.limit)
            .map(|(route, limit)| limit::bucket(route, limit));
        let result = match &req.upstream {
            Some(address) => {
                tokio::time::timeout(
                    TIMEOUT,
                    exchange(&upstream(address), &req, limit.as_ref(), &mut c),
                )
                .await
            }
            None => Ok(Err(invalid("no X-Isureal-Upstream"))),
        };
        let ok = matches!(result, Ok(Ok(())));
        match result {
            Ok(Ok(())) => {}
            Ok(Err(_)) => {
                c.out.clear();
                c.out.extend_from_slice(
                    b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
                );
            }
            Err(_) => {
                c.out.clear();
                c.out.extend_from_slice(b"HTTP/1.1 504 Gateway Timeout\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
            }
        }
        down.write_all(&c.out).await?;
        // An export can be large; do not keep its buffers for every connection.
        if c.body.capacity() > 16 << 20 {
            c.body = Vec::new();
            c.zip = Vec::new();
        }
        if req.close || !ok {
            return Ok(());
        }
    }
}

fn env(key: &str, default: &str) -> String {
    std::env::var(key)
        .ok()
        .filter(|v| !v.is_empty())
        .unwrap_or_else(|| default.to_string())
}

fn main() {
    let listen = env("ROUTER_DIGEST_ADDRESS", "127.0.0.1:18010");
    let runtime = env("ROUTER_HAPROXY_RUNTIME_ADDRESS", "127.0.0.1:18001");
    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .expect("tokio runtime");
    rt.block_on(async move {
        let listener = match TcpListener::bind(&listen).await {
            Ok(l) => l,
            Err(e) => {
                eprintln!("isureal-digest: listen on {listen}: {e}");
                std::process::exit(1);
            }
        };
        eprintln!("isureal-digest: listening on {listen}, HAProxy runtime API {runtime}");
        tokio::spawn(limit::poll_haproxy(runtime));
        tokio::spawn(async {
            let mut last = 0;
            loop {
                tokio::time::sleep(Duration::from_secs(10)).await;
                let n = REQUESTS.load(Ordering::Relaxed);
                if n != last {
                    eprintln!(
                        "isureal-digest: requests {} digested_bytes {}",
                        n,
                        DIGESTED_BYTES.load(Ordering::Relaxed)
                    );
                    last = n;
                }
            }
        });
        loop {
            match listener.accept().await {
                Ok((s, _)) => {
                    tokio::spawn(async move {
                        let _ = serve(s).await;
                    });
                }
                Err(e) => {
                    eprintln!("isureal-digest: accept: {e}");
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            }
        }
    });
}
