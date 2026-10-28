//! Buffered HTTP/1.1 reading: heads, lines and bodies of every framing.

use std::io;
use std::sync::Arc;

use tokio::io::AsyncReadExt;
use tokio::net::TcpStream;
use xxhash_rust::xxh3::Xxh3;

use crate::limit::Bucket;

/// The largest body collected in memory (an export).
pub const MAX_BODY: usize = 256 << 20;

pub fn invalid(msg: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, msg)
}

pub fn eof() -> io::Error {
    io::Error::new(io::ErrorKind::UnexpectedEof, "unexpected EOF")
}

/// A read buffer reused across the requests of one connection.
pub struct Buf {
    data: Vec<u8>,
    pos: usize,
    end: usize,
    /// Throttles the reads while set (the upstream body).
    pub limit: Option<Arc<Bucket>>,
}

impl Buf {
    pub fn new(cap: usize) -> Self {
        Buf {
            data: vec![0; cap],
            pos: 0,
            end: 0,
            limit: None,
        }
    }

    pub fn avail(&self) -> &[u8] {
        &self.data[self.pos..self.end]
    }

    pub fn consume(&mut self, n: usize) {
        self.pos += n;
        if self.pos == self.end {
            self.pos = 0;
            self.end = 0;
        }
    }

    pub fn clear(&mut self) {
        self.pos = 0;
        self.end = 0;
        self.limit = None;
    }

    pub async fn fill(&mut self, s: &mut TcpStream) -> io::Result<usize> {
        if self.end == self.data.len() {
            if self.pos == 0 {
                return Err(invalid("line too long"));
            }
            self.data.copy_within(self.pos..self.end, 0);
            self.end -= self.pos;
            self.pos = 0;
        }
        let space = self.data.len() - self.end;
        let Some(limit) = &self.limit else {
            let n = s.read(&mut self.data[self.end..]).await?;
            self.end += n;
            return Ok(n);
        };
        let grant = limit.acquire(space).await;
        let read = s.read(&mut self.data[self.end..self.end + grant]).await;
        let n = *read.as_ref().unwrap_or(&0);
        if n < grant {
            limit.charge(-((grant - n) as f64));
        }
        self.end += n;
        read
    }

    /// Reads until a head ending in CRLF CRLF is buffered and returns its length,
    /// or None on EOF before any byte.
    pub async fn read_head(&mut self, s: &mut TcpStream) -> io::Result<Option<usize>> {
        let mut from = 0;
        loop {
            if let Some(i) = memchr::memmem::find(&self.avail()[from..], b"\r\n\r\n") {
                return Ok(Some(from + i + 4));
            }
            from = self.avail().len().saturating_sub(3);
            if self.fill(s).await? == 0 {
                return if self.avail().is_empty() {
                    Ok(None)
                } else {
                    Err(eof())
                };
            }
        }
    }

    async fn read_line(&mut self, s: &mut TcpStream) -> io::Result<usize> {
        loop {
            if let Some(i) = memchr::memchr(b'\n', self.avail()) {
                return Ok(i + 1);
            }
            if self.fill(s).await? == 0 {
                return Err(eof());
            }
        }
    }
}

/// Where a body goes: into a hash or into memory.
pub enum Sink<'a> {
    Hash(&'a mut Xxh3, &'a mut u64),
    Collect(&'a mut Vec<u8>),
}

impl Sink<'_> {
    fn write(&mut self, b: &[u8]) -> io::Result<()> {
        match self {
            Sink::Hash(h, n) => {
                h.update(b);
                **n += b.len() as u64;
            }
            Sink::Collect(v) => {
                if v.len() + b.len() > MAX_BODY {
                    return Err(invalid("body too large"));
                }
                v.extend_from_slice(b);
            }
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Framing {
    Empty,
    Length(u64),
    Chunked,
    Close,
}

pub async fn read_exact_to(
    buf: &mut Buf,
    s: &mut TcpStream,
    mut n: u64,
    sink: &mut Sink<'_>,
) -> io::Result<()> {
    while n > 0 {
        if buf.avail().is_empty() && buf.fill(s).await? == 0 {
            return Err(eof());
        }
        let take = (buf.avail().len() as u64).min(n) as usize;
        sink.write(&buf.avail()[..take])?;
        buf.consume(take);
        n -= take as u64;
    }
    Ok(())
}

pub fn chunk_size(line: &[u8]) -> io::Result<u64> {
    let mut size: u64 = 0;
    let mut digits = 0;
    for &c in line {
        let d = match c {
            b'0'..=b'9' => c - b'0',
            b'a'..=b'f' => c - b'a' + 10,
            b'A'..=b'F' => c - b'A' + 10,
            _ => break,
        };
        size = size.checked_mul(16).ok_or_else(|| invalid("chunk size"))? + d as u64;
        digits += 1;
    }
    if digits == 0 {
        return Err(invalid("chunk size"));
    }
    Ok(size)
}

pub async fn read_body(
    buf: &mut Buf,
    s: &mut TcpStream,
    framing: Framing,
    sink: &mut Sink<'_>,
) -> io::Result<()> {
    match framing {
        Framing::Empty => Ok(()),
        Framing::Length(n) => read_exact_to(buf, s, n, sink).await,
        Framing::Close => loop {
            if !buf.avail().is_empty() {
                let n = buf.avail().len();
                sink.write(buf.avail())?;
                buf.consume(n);
            }
            if buf.fill(s).await? == 0 {
                return Ok(());
            }
        },
        Framing::Chunked => loop {
            let len = buf.read_line(s).await?;
            let size = chunk_size(&buf.avail()[..len])?;
            buf.consume(len);
            if size == 0 {
                loop {
                    let len = buf.read_line(s).await?;
                    buf.consume(len);
                    if len <= 2 {
                        return Ok(());
                    }
                }
            }
            read_exact_to(buf, s, size, sink).await?;
            let len = buf.read_line(s).await?;
            if len > 2 {
                return Err(invalid("chunk end"));
            }
            buf.consume(len);
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn chunk_sizes() {
        assert_eq!(chunk_size(b"1a2B\r\n").unwrap(), 0x1a2b);
        assert_eq!(chunk_size(b"10;ext=1\r\n").unwrap(), 16);
        assert!(chunk_size(b"\r\n").is_err());
        assert!(chunk_size(b"ffffffffffffffffff\r\n").is_err());
    }
}
