//! Connection handling: request heads, body framing, keep-alive, response
//! serialization and TLS. One thread per connection, bounded in number.

use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, RwLock};
use std::time::{Duration, Instant};

use rustls::server::{ClientHello, ResolvesServerCert};
use rustls::sign::CertifiedKey;

use super::{Body, Handler, Headers, Request, Response, ResponseBody, canonical_key, status_text, unescape};
use crate::shutdown::{InFlight, Shutdown};

/// Where a request body is read from; tests use an in-memory cursor.
pub trait BodySource: Read {
    /// The instant after which no further socket read may start or complete.
    fn set_read_deadline(&mut self, deadline: Option<Instant>);
    fn send_continue(&mut self) -> io::Result<()>;
}

impl BodySource for io::Cursor<Vec<u8>> {
    fn set_read_deadline(&mut self, _: Option<Instant>) {}
    fn send_continue(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn deadline_passed() -> io::Error {
    io::Error::new(io::ErrorKind::TimedOut, "i/o timeout")
}

/// A TCP socket whose every receive is bounded by one absolute deadline.
///
/// A per-call socket timeout alone bounds inactivity, not duration: a peer
/// that sends one byte just inside each timeout keeps a read loop (a TLS
/// record, a chunk-size line, a trailer block) alive indefinitely. Arming the
/// remaining time before each receive makes the deadline hold below TLS and
/// below every framing loop, so no caller has to re-check it.
struct DeadlineSocket {
    sock: TcpStream,
    read_deadline: Option<Instant>,
}

impl Read for DeadlineSocket {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if let Some(deadline) = self.read_deadline {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(deadline_passed());
            }
            self.sock.set_read_timeout(Some(remaining))?;
        }
        self.sock.read(buf)
    }
}

impl Write for DeadlineSocket {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.sock.write(buf)
    }
    fn flush(&mut self) -> io::Result<()> {
        self.sock.flush()
    }
}

trait Stream: Read + Write + Send {
    fn set_read_deadline(&mut self, deadline: Option<Instant>);
    /// Sends FIN (after TLS close_notify) without discarding unread input.
    fn close_write(&mut self);
}

impl Stream for DeadlineSocket {
    fn set_read_deadline(&mut self, deadline: Option<Instant>) {
        self.read_deadline = deadline;
    }
    fn close_write(&mut self) {
        let _ = self.sock.shutdown(std::net::Shutdown::Write);
    }
}

impl Stream for rustls::StreamOwned<rustls::ServerConnection, DeadlineSocket> {
    fn set_read_deadline(&mut self, deadline: Option<Instant>) {
        self.sock.read_deadline = deadline;
    }
    fn close_write(&mut self) {
        self.conn.send_close_notify();
        let _ = self.flush();
        let _ = self.sock.sock.shutdown(std::net::Shutdown::Write);
    }
}

/// Completes the TLS handshake under one total deadline. `StreamOwned` would
/// otherwise run it lazily inside the first read, where only the per-receive
/// timeout applies.
fn complete_handshake(tls: &mut rustls::StreamOwned<rustls::ServerConnection, DeadlineSocket>, deadline: Instant) -> io::Result<()> {
    tls.sock.read_deadline = Some(deadline);
    while tls.conn.is_handshaking() {
        tls.conn.complete_io(&mut tls.sock)?;
    }
    Ok(())
}

struct Conn {
    reader: BufReader<Box<dyn Stream>>,
}

impl Read for Conn {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        self.reader.read(buf)
    }
}

impl BodySource for Conn {
    fn set_read_deadline(&mut self, deadline: Option<Instant>) {
        self.reader.get_mut().set_read_deadline(deadline);
    }
    fn send_continue(&mut self) -> io::Result<()> {
        let stream = self.reader.get_mut();
        stream.write_all(b"HTTP/1.1 100 Continue\r\n\r\n")?;
        stream.flush()
    }
}

/// All trailer lines of one chunked message, including their line breaks.
const MAX_TRAILER_BYTES: usize = 8 << 10;

#[derive(Clone, Debug, Default)]
pub struct ChunkState {
    remaining: u64,
    need_crlf: bool,
    pub(crate) done: bool,
}

fn read_line_from(source: &mut dyn BodySource, limit: usize) -> io::Result<String> {
    let mut line = Vec::new();
    let mut byte = [0u8; 1];
    loop {
        if source.read(&mut byte)? == 0 {
            return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "unexpected EOF"));
        }
        if byte[0] == b'\n' {
            break;
        }
        line.push(byte[0]);
        if line.len() > limit + 1 {
            return Err(io::Error::new(io::ErrorKind::InvalidData, "http chunk length too large"));
        }
    }
    if line.last() == Some(&b'\r') {
        line.pop();
    }
    Ok(String::from_utf8_lossy(&line).into_owned())
}

pub(crate) fn read_chunked(source: &mut dyn BodySource, state: &mut ChunkState, buf: &mut [u8]) -> io::Result<usize> {
    if state.done {
        return Ok(0);
    }
    if state.remaining == 0 {
        if state.need_crlf {
            let end = read_line_from(source, 0)?;
            if !end.is_empty() {
                return Err(io::Error::new(io::ErrorKind::InvalidData, "malformed chunked encoding"));
            }
            state.need_crlf = false;
        }
        let line = read_line_from(source, 4096)?;
        let size_text = line.split(';').next().unwrap_or("").trim();
        let size = u64::from_str_radix(size_text, 16).map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid byte in chunk length"))?;
        if size == 0 {
            // Trailers carry nothing the API uses; a peer may not keep the
            // message open with an unbounded run of them.
            let mut trailer_bytes = 0usize;
            loop {
                let line = read_line_from(source, 4096)?;
                if line.is_empty() {
                    break;
                }
                trailer_bytes += line.len() + 2;
                if trailer_bytes > MAX_TRAILER_BYTES {
                    return Err(io::Error::new(io::ErrorKind::InvalidData, "http trailers too large"));
                }
            }
            state.done = true;
            return Ok(0);
        }
        state.remaining = size;
        state.need_crlf = true;
    }
    let want = buf.len().min(state.remaining.min(usize::MAX as u64) as usize);
    let n = source.read(&mut buf[..want])?;
    if n == 0 {
        return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "unexpected EOF"));
    }
    state.remaining -= n as u64;
    Ok(n)
}

#[derive(Clone, Debug)]
pub struct ServerOptions {
    pub read_header_timeout: Duration,
    /// The whole request body, unless a handler extends the deadline.
    pub read_timeout: Duration,
    pub idle_timeout: Duration,
    pub write_timeout: Option<Duration>,
    /// The whole TLS handshake, from accept to the first request byte.
    pub handshake_timeout: Duration,
    pub max_header_bytes: usize,
    pub max_header_count: usize,
    pub max_connections: usize,
    /// Concurrent connections from one source address. One client can no
    /// longer occupy every worker; a client with many addresses still can.
    pub max_connections_per_source: usize,
    pub shutdown_drain: Duration,
}

impl Default for ServerOptions {
    fn default() -> Self {
        ServerOptions {
            read_header_timeout: Duration::from_secs(5),
            read_timeout: Duration::from_secs(10),
            idle_timeout: Duration::from_secs(15),
            write_timeout: Some(Duration::from_secs(30)),
            handshake_timeout: Duration::from_secs(10),
            max_header_bytes: 1 << 20,
            max_header_count: 128,
            max_connections: 128,
            max_connections_per_source: 32,
            shutdown_drain: Duration::from_secs(8),
        }
    }
}

/// Serves until `shutdown` starts, then waits up to `shutdown_drain` for
/// in-flight connections, like `http.Server.Shutdown`.
pub fn serve(listener: TcpListener, tls: Option<Arc<rustls::ServerConfig>>, handler: Handler, options: ServerOptions, shutdown: &Shutdown) -> io::Result<()> {
    let acceptor = Acceptor::new(listener, shutdown)?;
    let active = Arc::new(InFlight::default());
    let sources = Arc::new(SourceLimiter::default());
    while let Some((stream, peer)) = acceptor.next(shutdown)? {
        if active.len() >= options.max_connections {
            drop(stream);
            continue;
        }
        let Some(source_slot) = sources.enter(peer.ip(), options.max_connections_per_source) else {
            drop(stream);
            continue;
        };
        let _ = stream.set_nodelay(true);
        let in_flight = active.enter();
        let (handler, options, tls) = (handler.clone(), options.clone(), tls.clone());
        std::thread::spawn(move || {
            // Both are released on unwind as well as on return.
            let (_in_flight, _source_slot) = (in_flight, source_slot);
            let local = stream.local_addr().map(|a| a.to_string()).unwrap_or_default();
            let remote = peer.to_string();
            let _ = stream.set_write_timeout(options.write_timeout);
            let socket = DeadlineSocket { sock: stream, read_deadline: None };
            let boxed: Option<Box<dyn Stream>> = match tls {
                Some(config) => rustls::ServerConnection::new(config).ok().and_then(|conn| {
                    let mut tls = rustls::StreamOwned::new(conn, socket);
                    complete_handshake(&mut tls, Instant::now() + options.handshake_timeout).ok()?;
                    Some(Box::new(tls) as Box<dyn Stream>)
                }),
                None => Some(Box::new(socket)),
            };
            if let Some(boxed) = boxed {
                serve_connection(boxed, &remote, &local, &handler, &options);
            }
        });
    }
    active.wait_idle(options.shutdown_drain);
    Ok(())
}

/// Concurrent connections per source address. An IPv4 peer on the dual-stack
/// listener is counted under its IPv4 address.
#[derive(Default)]
struct SourceLimiter {
    counts: std::sync::Mutex<std::collections::HashMap<std::net::IpAddr, usize>>,
}

struct SourceSlot {
    limiter: Arc<SourceLimiter>,
    source: std::net::IpAddr,
}

impl SourceLimiter {
    fn enter(self: &Arc<Self>, source: std::net::IpAddr, limit: usize) -> Option<SourceSlot> {
        let source = source.to_canonical();
        let mut counts = self.counts.lock().unwrap_or_else(|e| e.into_inner());
        let count = counts.get(&source).copied().unwrap_or(0);
        if count >= limit {
            return None;
        }
        counts.insert(source, count + 1);
        Some(SourceSlot { limiter: self.clone(), source })
    }
}

impl Drop for SourceSlot {
    fn drop(&mut self) {
        let mut counts = self.limiter.counts.lock().unwrap_or_else(|e| e.into_inner());
        match counts.get_mut(&self.source) {
            Some(count) if *count > 1 => *count -= 1,
            _ => {
                counts.remove(&self.source);
            }
        }
    }
}

/// Accepts connections until shutdown. Between connections the thread sleeps
/// in `poll(2)` on the listener and a pipe that the shutdown writes to, so an
/// idle server never wakes up.
struct Acceptor {
    listener: TcpListener,
    #[cfg(unix)]
    wake: io::PipeReader,
}

impl Acceptor {
    fn new(listener: TcpListener, shutdown: &Shutdown) -> io::Result<Acceptor> {
        listener.set_nonblocking(true)?;
        #[cfg(unix)]
        {
            let (wake, writer) = io::pipe()?;
            shutdown.on_trigger(move || {
                let _ = (&writer).write_all(&[1]);
            });
            Ok(Acceptor { listener, wake })
        }
        #[cfg(not(unix))]
        {
            let _ = shutdown;
            Ok(Acceptor { listener })
        }
    }

    fn next(&self, shutdown: &Shutdown) -> io::Result<Option<(TcpStream, std::net::SocketAddr)>> {
        loop {
            if shutdown.is_triggered() {
                return Ok(None);
            }
            match self.listener.accept() {
                Ok((stream, peer)) => {
                    // Accepted sockets inherit non-blocking mode on some systems.
                    stream.set_nonblocking(false)?;
                    return Ok(Some((stream, peer)));
                }
                Err(err) if err.kind() == io::ErrorKind::WouldBlock => self.wait(shutdown)?,
                Err(err) if err.kind() == io::ErrorKind::Interrupted => {}
                Err(err) => {
                    // Usually descriptor exhaustion; back off instead of spinning.
                    log::info!("http: Accept error: {}; retrying", err);
                    shutdown.sleep(Duration::from_millis(50));
                }
            }
        }
    }

    #[cfg(unix)]
    fn wait(&self, _: &Shutdown) -> io::Result<()> {
        use std::os::fd::AsFd;
        crate::sys::wait_readable(&[self.listener.as_fd(), self.wake.as_fd()]).map(drop)
    }

    /// Development hosts without `poll(2)` look again shortly.
    #[cfg(not(unix))]
    fn wait(&self, shutdown: &Shutdown) -> io::Result<()> {
        shutdown.sleep(Duration::from_millis(50));
        Ok(())
    }
}

enum HeadError {
    Closed,
    BadRequest(&'static str),
    TooLarge,
    NotImplemented,
}

struct Head {
    method: String,
    target: String,
    proto: String,
    headers: Headers,
}

fn read_head(reader: &mut BufReader<Box<dyn Stream>>, options: &ServerOptions, first: bool) -> Result<Head, HeadError> {
    // Wait for the next request under the idle timeout, then bound the head.
    let wait = if first { options.read_header_timeout } else { options.idle_timeout };
    reader.get_mut().set_read_deadline(Some(Instant::now() + wait));
    if reader.fill_buf().map(|b| b.is_empty()).unwrap_or(true) {
        return Err(HeadError::Closed);
    }
    let deadline = Instant::now() + options.read_header_timeout;
    reader.get_mut().set_read_deadline(Some(deadline));
    let mut total = 0usize;
    let mut lines: Vec<String> = Vec::new();
    loop {
        if deadline.saturating_duration_since(Instant::now()).is_zero() {
            return Err(HeadError::Closed);
        }
        let mut line = Vec::new();
        let n = reader.by_ref().take((options.max_header_bytes + 4096 - total) as u64 + 1).read_until(b'\n', &mut line).map_err(|_| HeadError::Closed)?;
        if n == 0 {
            return Err(HeadError::Closed);
        }
        total += n;
        if total > options.max_header_bytes + 4096 {
            return Err(HeadError::TooLarge);
        }
        if line.last() != Some(&b'\n') {
            return Err(HeadError::Closed);
        }
        line.pop();
        if line.last() == Some(&b'\r') {
            line.pop();
        }
        if line.is_empty() {
            if lines.is_empty() {
                continue; // tolerate leading blank lines
            }
            break;
        }
        // The request line plus the header fields.
        if lines.len() > options.max_header_count {
            return Err(HeadError::TooLarge);
        }
        lines.push(String::from_utf8_lossy(&line).into_owned());
    }
    let mut parts = lines[0].splitn(3, ' ');
    let (method, target, proto) = (parts.next().unwrap_or(""), parts.next().unwrap_or(""), parts.next().unwrap_or(""));
    if method.is_empty() || target.is_empty() || !(proto == "HTTP/1.1" || proto == "HTTP/1.0") {
        return Err(HeadError::BadRequest("malformed HTTP request"));
    }
    if !method.bytes().all(|b| b.is_ascii_alphanumeric() || b"!#$%&'*+-.^_`|~".contains(&b)) {
        return Err(HeadError::BadRequest("invalid method"));
    }
    let mut headers = Headers::default();
    for line in &lines[1..] {
        if line.starts_with(' ') || line.starts_with('\t') {
            return Err(HeadError::BadRequest("malformed MIME header line"));
        }
        let (name, value) = line.split_once(':').ok_or(HeadError::BadRequest("malformed MIME header line"))?;
        if !super::valid_header_name(name) {
            return Err(HeadError::BadRequest("malformed MIME header line"));
        }
        headers.add(&canonical_key(name), value.trim_matches([' ', '\t']).to_string());
    }
    let te = headers.get("Transfer-Encoding").to_ascii_lowercase();
    if headers.has("Transfer-Encoding") && headers.has("Content-Length") {
        return Err(HeadError::BadRequest("message cannot contain both Transfer-Encoding and Content-Length"));
    }
    if headers.values("Transfer-Encoding").len() > 1 {
        return Err(HeadError::BadRequest("message cannot contain multiple Transfer-Encoding headers"));
    }
    if !te.is_empty() && te != "chunked" {
        return Err(HeadError::NotImplemented);
    }
    if headers.values("Content-Length").iter().collect::<std::collections::HashSet<_>>().len() > 1 {
        return Err(HeadError::BadRequest("message cannot contain multiple Content-Length headers"));
    }
    Ok(Head { method: method.into(), target: target.into(), proto: proto.into(), headers })
}

fn simple_error(stream: &mut dyn Write, status: u16, message: &str) {
    let body = format!("{} {}", status, message);
    let _ = write!(stream, "HTTP/1.1 {} {}\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n{}", status, status_text(status), body);
    let _ = stream.flush();
}

fn serve_connection(stream: Box<dyn Stream>, remote: &str, local: &str, handler: &Handler, options: &ServerOptions) {
    let mut conn = Conn { reader: BufReader::with_capacity(16 * 1024, stream) };
    let mut first = true;
    loop {
        let head = match read_head(&mut conn.reader, options, first) {
            Ok(head) => head,
            Err(HeadError::Closed) => return,
            Err(HeadError::TooLarge) => return simple_error(conn.reader.get_mut(), 431, "Request Header Fields Too Large"),
            Err(HeadError::NotImplemented) => return simple_error(conn.reader.get_mut(), 501, "Unsupported transfer encoding"),
            Err(HeadError::BadRequest(reason)) => return simple_error(conn.reader.get_mut(), 400, &format!("Bad Request: {}", reason)),
        };
        first = false;
        let started = Instant::now();
        let target = head.target.clone();
        let (raw_path, raw_query) = if target == "*" {
            ("*".to_string(), String::new())
        } else if let Some(rest) = target.strip_prefix("http://").or_else(|| target.strip_prefix("https://")) {
            let path_start = rest.find('/').unwrap_or(rest.len());
            let path = &rest[path_start..];
            let (p, q) = path.split_once('?').unwrap_or((path, ""));
            (if p.is_empty() { "/".to_string() } else { p.to_string() }, q.to_string())
        } else if target.starts_with('/') {
            let (p, q) = target.split_once('?').unwrap_or((&target, ""));
            (p.to_string(), q.to_string())
        } else {
            return simple_error(conn.reader.get_mut(), 400, "Bad Request");
        };
        let Some(path) = unescape(&raw_path, false) else {
            return simple_error(conn.reader.get_mut(), 400, "Bad Request");
        };
        let chunked = head.headers.get("Transfer-Encoding").eq_ignore_ascii_case("chunked");
        let content_length: i64 = if chunked {
            -1
        } else {
            match head.headers.get("Content-Length").trim() {
                "" => 0,
                value => match value.parse::<u64>().ok().and_then(|n| i64::try_from(n).ok()) {
                    Some(n) => n,
                    None => return simple_error(conn.reader.get_mut(), 400, "Bad Request: bad Content-Length"),
                },
            }
        };
        let host = head.headers.get("Host").to_string();
        if head.proto == "HTTP/1.1" && !head.headers.has("Host") {
            return simple_error(conn.reader.get_mut(), 400, "Bad Request: missing required Host header");
        }
        let connection = head.headers.get("Connection").to_ascii_lowercase();
        let mut close = connection.contains("close") || (head.proto == "HTTP/1.0" && !connection.contains("keep-alive"));
        let expects_continue = head.headers.get("Expect").eq_ignore_ascii_case("100-continue") && head.proto == "HTTP/1.1";
        let method = head.method.clone();
        let (response, body_finished, body_exceeded) = {
            let mut request = Request {
                method: head.method,
                path,
                raw_path,
                raw_query,
                proto: head.proto,
                headers: head.headers,
                host,
                remote_addr: remote.to_string(),
                local_addr: local.to_string(),
                content_length,
                params: Vec::new(),
                session: None,
                body: Body {
                    source: Some(&mut conn),
                    remaining: content_length.max(0) as u64,
                    chunked: chunked.then(ChunkState::default),
                    limit: None,
                    consumed: 0,
                    deadline: started + options.read_timeout,
                    continue_pending: expects_continue && content_length != 0,
                    exceeded: false,
                },
            };
            let response = handler(&mut request);
            // Drain a small unread remainder so the connection can be reused.
            let mut finished = request.body.finished();
            if !finished && !request.body.continue_pending && !request.body.exceeded {
                request.body.limit = None;
                let mut sink = [0u8; 8192];
                let mut drained = 0usize;
                while drained < 256 * 1024 {
                    match request.body.read(&mut sink) {
                        Ok(0) => break,
                        Ok(n) => drained += n,
                        Err(_) => break,
                    }
                }
                finished = request.body.finished();
            }
            (response, finished, request.body.exceeded)
        };
        if !body_finished || body_exceeded {
            close = true;
        }
        if write_response(conn.reader.get_mut(), &method, response, close).is_err() {
            return;
        }
        if close {
            return linger_close(&mut conn);
        }
    }
}

/// net/http's closeWriteAndWait: FIN first, then briefly discard input so
/// unread request bytes do not trigger an RST that destroys the response.
fn linger_close(conn: &mut Conn) {
    conn.reader.get_mut().close_write();
    let deadline = Instant::now() + Duration::from_millis(500);
    conn.reader.get_mut().set_read_deadline(Some(deadline));
    let mut sink = [0u8; 4096];
    while Instant::now() < deadline {
        match conn.reader.get_mut().read(&mut sink) {
            Ok(0) | Err(_) => break,
            Ok(_) => {}
        }
    }
}

/// http.TimeFormat.
fn http_date() -> String {
    chrono::Utc::now().format("%a, %d %b %Y %H:%M:%S GMT").to_string()
}

fn sniff_content_type(body: &[u8]) -> &'static str {
    // Go's text signature permits TAB, LF, FF, CR and ESC, but not VT.
    let text = std::str::from_utf8(body).is_ok() && !body.iter().any(|&b| b < 0x09 || b == 0x0b || ((0x0e..0x20).contains(&b) && b != 0x1b));
    let trimmed = body.iter().skip_while(|b| b.is_ascii_whitespace()).take(14).map(|b| b.to_ascii_lowercase()).collect::<Vec<u8>>();
    if trimmed.starts_with(b"<!doctype html") || trimmed.starts_with(b"<html") {
        "text/html; charset=utf-8"
    } else if text {
        "text/plain; charset=utf-8"
    } else {
        "application/octet-stream"
    }
}

fn write_response(stream: &mut dyn Write, method: &str, mut response: Response, close: bool) -> io::Result<()> {
    let status = response.status;
    let bodyless = (100..200).contains(&status) || status == 204 || status == 304;
    if !response.headers.has("Date") {
        response.headers.set("Date", http_date());
    }
    let mut out = Vec::with_capacity(512);
    write!(out, "HTTP/1.1 {} {}\r\n", status, status_text(status))?;
    let mut stream_body: Option<Box<dyn Read + Send>> = None;
    let mut fixed: Vec<u8> = Vec::new();
    match std::mem::replace(&mut response.body, ResponseBody::Bytes(Vec::new())) {
        ResponseBody::Bytes(bytes) => {
            if !bodyless {
                if !response.headers.has("Content-Type") && !bytes.is_empty() {
                    response.headers.set("Content-Type", sniff_content_type(&bytes));
                }
                if !response.headers.has("Content-Length") {
                    response.headers.set("Content-Length", bytes.len().to_string());
                }
            }
            fixed = bytes;
        }
        ResponseBody::Stream(reader) => {
            if !bodyless && !response.headers.has("Content-Length") {
                response.headers.set("Transfer-Encoding", "chunked");
            }
            stream_body = Some(reader);
        }
    }
    if close {
        response.headers.set("Connection", "close");
    }
    for (name, value) in response.headers.iter() {
        // A header value never carries a line break onto the wire.
        let value: String = value.chars().map(|c| if c == '\r' || c == '\n' { ' ' } else { c }).collect();
        write!(out, "{}: {}\r\n", name, value)?;
    }
    out.extend_from_slice(b"\r\n");
    let send_body = method != "HEAD" && !bodyless;
    if send_body {
        out.extend_from_slice(&fixed);
    }
    stream.write_all(&out)?;
    if let (true, Some(mut reader)) = (send_body, stream_body) {
        let chunked = response.headers.get("Transfer-Encoding") == "chunked";
        let mut buf = vec![0u8; 32 * 1024];
        loop {
            let n = reader.read(&mut buf)?;
            if n == 0 {
                break;
            }
            if chunked {
                write!(stream, "{:x}\r\n", n)?;
                stream.write_all(&buf[..n])?;
                stream.write_all(b"\r\n")?;
            } else {
                stream.write_all(&buf[..n])?;
            }
            stream.flush()?;
        }
        if chunked {
            stream.write_all(b"0\r\n\r\n")?;
        }
    }
    stream.flush()
}

/// tls.Config.GetCertificate: the current certificate, optionally refreshed
/// on each handshake so a rotated certificate is served without a restart.
pub struct CertResolver {
    current: RwLock<Arc<CertifiedKey>>,
    refresh: Option<Box<dyn Fn() -> Option<(Vec<u8>, Vec<u8>)> + Send + Sync>>,
}

impl std::fmt::Debug for CertResolver {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("CertResolver")
    }
}

fn certified_key(cert_pem: &[u8], key_pem: &[u8]) -> crate::goish::errors::Result<Arc<CertifiedKey>> {
    use rustls::pki_types::{CertificateDer, PrivateKeyDer, PrivatePkcs1KeyDer, PrivatePkcs8KeyDer, PrivateSec1KeyDer};
    let certs: Vec<CertificateDer<'static>> =
        pem::parse_many(cert_pem)?.into_iter().filter(|p| p.tag() == "CERTIFICATE").map(|p| CertificateDer::from(p.into_contents())).collect();
    if certs.is_empty() {
        return Err(crate::errorf!("tls: failed to find any PEM data in certificate input"));
    }
    let block = pem::parse_many(key_pem)?
        .into_iter()
        .find(|p| p.tag().ends_with("PRIVATE KEY"))
        .ok_or_else(|| crate::errorf!("tls: failed to find any PEM data in key input"))?;
    let key: PrivateKeyDer<'static> = match block.tag() {
        "EC PRIVATE KEY" => PrivateSec1KeyDer::from(block.into_contents()).into(),
        "RSA PRIVATE KEY" => PrivatePkcs1KeyDer::from(block.into_contents()).into(),
        _ => PrivatePkcs8KeyDer::from(block.into_contents()).into(),
    };
    let signing = rustls::crypto::ring::sign::any_supported_type(&key).map_err(|e| crate::errorf!("tls: {}", e))?;
    Ok(Arc::new(CertifiedKey::new(certs, signing)))
}

impl CertResolver {
    pub fn from_pem(cert_pem: &[u8], key_pem: &[u8]) -> crate::goish::errors::Result<CertResolver> {
        Ok(CertResolver { current: RwLock::new(certified_key(cert_pem, key_pem)?), refresh: None })
    }

    pub fn with_refresh(mut self, refresh: Box<dyn Fn() -> Option<(Vec<u8>, Vec<u8>)> + Send + Sync>) -> CertResolver {
        self.refresh = Some(refresh);
        self
    }

    pub fn set_pem(&self, cert_pem: &[u8], key_pem: &[u8]) -> crate::goish::errors::Result<()> {
        *self.current.write().unwrap_or_else(|e| e.into_inner()) = certified_key(cert_pem, key_pem)?;
        Ok(())
    }

    /// TLS 1.2+ with the ring provider and HTTP/1.1 ALPN.
    pub fn server_config(self: Arc<Self>) -> crate::goish::errors::Result<Arc<rustls::ServerConfig>> {
        let provider = Arc::new(rustls::crypto::ring::default_provider());
        let mut config = rustls::ServerConfig::builder_with_provider(provider)
            .with_protocol_versions(&[&rustls::version::TLS13, &rustls::version::TLS12])
            .map_err(|e| crate::errorf!("{}", e))?
            .with_no_client_auth()
            .with_cert_resolver(self);
        config.alpn_protocols = vec![b"http/1.1".to_vec()];
        Ok(Arc::new(config))
    }
}

impl ResolvesServerCert for CertResolver {
    fn resolve(&self, _hello: ClientHello<'_>) -> Option<Arc<CertifiedKey>> {
        if let Some(refresh) = &self.refresh {
            if let Some((cert, key)) = refresh() {
                if let Ok(next) = certified_key(&cert, &key) {
                    *self.current.write().unwrap_or_else(|e| e.into_inner()) = next;
                }
            }
        }
        Some(self.current.read().unwrap_or_else(|e| e.into_inner()).clone())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::httpd::Router;

    #[test]
    fn control_bytes_and_header_names_cannot_be_misclassified() {
        assert_eq!(sniff_content_type(b"a\x0bb"), "application/octet-stream");
        assert_eq!(sniff_content_type(b"a\t\n\x0c\r\x1bb"), "text/plain; charset=utf-8");
        let mut headers = Headers::default();
        for name in ["X-Bad\r\nInjected", "X:Bad", "", "Bad Name"] {
            headers.set(name, "value");
            headers.add(name, "value");
        }
        assert_eq!(headers.iter().count(), 0);
    }

    #[test]
    fn nonreading_peer_releases_response_writer() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let mut peer = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        let (stream, _) = listener.accept().unwrap();
        let (done, completion) = std::sync::mpsc::channel();
        let server = std::thread::spawn(move || {
            let handler: Handler = Arc::new(|_| Response::bytes(200, "application/octet-stream", vec![0; 32 << 20]));
            let options = ServerOptions { write_timeout: Some(Duration::from_millis(100)), ..Default::default() };
            stream.set_write_timeout(options.write_timeout).unwrap();
            serve_connection(Box::new(DeadlineSocket { sock: stream, read_deadline: None }), "127.0.0.1:1", "127.0.0.1:2", &handler, &options);
            let _ = done.send(());
        });
        peer.write_all(b"GET / HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n").unwrap();
        let result = completion.recv_timeout(Duration::from_secs(5));
        drop(peer);
        server.join().unwrap();
        assert!(result.is_ok(), "nonreading client retained its connection worker");
        assert_eq!(ServerOptions::default().write_timeout, Some(Duration::from_secs(30)));
    }

    fn serve_with(options: ServerOptions, tls: Option<Arc<rustls::ServerConfig>>) -> (std::net::SocketAddr, Shutdown, std::thread::JoinHandle<()>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        // Answers at once, like the API gate does for an unauthenticated request.
        let handler: Handler = Arc::new(|_| Response::error("Unauthorized", 401));
        let stop = Shutdown::new();
        let stop_server = stop.clone();
        let server = std::thread::spawn(move || serve(listener, tls, handler, options, &stop_server).unwrap());
        (addr, stop, server)
    }

    /// Feeds `piece` every 200 ms and reports how long the server kept the
    /// connection open, and everything it sent.
    fn dribble(stream: &mut TcpStream, piece: &[u8], give_up: Duration) -> (Duration, String) {
        stream.set_read_timeout(Some(Duration::from_millis(200))).unwrap();
        let started = Instant::now();
        let mut received = Vec::new();
        let mut buf = [0u8; 1024];
        while started.elapsed() < give_up {
            let _ = stream.write_all(piece);
            match stream.read(&mut buf) {
                Ok(0) => break,
                Ok(n) => received.extend_from_slice(&buf[..n]),
                Err(err) if matches!(err.kind(), io::ErrorKind::WouldBlock | io::ErrorKind::TimedOut) => {}
                Err(_) => break,
            }
        }
        (started.elapsed(), String::from_utf8_lossy(&received).into_owned())
    }

    #[test]
    fn dribbled_trailers_cannot_outlive_the_read_deadline() {
        let options = ServerOptions { read_timeout: Duration::from_secs(1), shutdown_drain: Duration::from_millis(200), ..Default::default() };
        let (addr, stop, server) = serve_with(options, None);
        let mut stream = TcpStream::connect(addr).unwrap();
        stream.write_all(b"POST /api/v1/auth/login HTTP/1.1\r\nHost: t\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n").unwrap();
        let (held, reply) = dribble(&mut stream, b"X-Pad: 1\r\n", Duration::from_secs(6));
        stop.trigger();
        server.join().unwrap();
        assert!(held < Duration::from_secs(4), "a dribbling peer held its worker for {held:?} past a 1s read deadline");
        assert!(reply.starts_with("HTTP/1.1 401") && reply.contains("Connection: close"), "unexpected reply ({} bytes)", reply.len());
    }

    #[test]
    fn dribbled_chunk_size_line_cannot_outlive_the_read_deadline() {
        let options = ServerOptions { read_timeout: Duration::from_secs(1), shutdown_drain: Duration::from_millis(200), ..Default::default() };
        let (addr, stop, server) = serve_with(options, None);
        let mut stream = TcpStream::connect(addr).unwrap();
        stream.write_all(b"POST /api/v1/auth/login HTTP/1.1\r\nHost: t\r\nTransfer-Encoding: chunked\r\n\r\n").unwrap();
        let (held, _) = dribble(&mut stream, b"0", Duration::from_secs(6));
        stop.trigger();
        server.join().unwrap();
        assert!(held < Duration::from_secs(4), "a dribbled chunk-size line held its worker for {held:?}");
    }

    #[test]
    fn trailer_block_is_bounded() {
        let run = |trailers: usize| {
            let mut wire = b"0\r\n".to_vec();
            for _ in 0..trailers {
                wire.extend_from_slice(b"X-Trailer: value\r\n");
            }
            wire.extend_from_slice(b"\r\n");
            let mut source = io::Cursor::new(wire);
            let mut state = ChunkState::default();
            read_chunked(&mut source, &mut state, &mut [0u8; 16]).map(|n| (n, state.done))
        };
        assert_eq!(run(3).unwrap(), (0, true));
        assert_eq!(run(MAX_TRAILER_BYTES / 18 + 1).unwrap_err().to_string(), "http trailers too large");
    }

    #[test]
    fn dribbled_tls_handshake_is_cut_at_the_handshake_deadline() {
        let dir = std::env::temp_dir().join(format!("mr-httpd-tls-{}-{}", std::process::id(), line!()));
        std::fs::create_dir_all(&dir).unwrap();
        let (cert, key) = crate::tlsutil::CertManager::new(dir.to_str().unwrap()).ensure_certificate(&crate::config::default_config()).unwrap();
        let tls = Arc::new(CertResolver::from_pem(&cert, &key).unwrap()).server_config().unwrap();
        let options = ServerOptions { handshake_timeout: Duration::from_secs(1), shutdown_drain: Duration::from_millis(200), ..Default::default() };
        let (addr, stop, server) = serve_with(options, Some(tls));
        let mut stream = TcpStream::connect(addr).unwrap();
        // A handshake record header announcing 512 bytes that then arrive one at a time.
        stream.write_all(&[0x16, 0x03, 0x01, 0x02, 0x00]).unwrap();
        let (held, _) = dribble(&mut stream, &[0x01], Duration::from_secs(6));
        stop.trigger();
        server.join().unwrap();
        std::fs::remove_dir_all(dir).ok();
        assert!(held < Duration::from_secs(4), "a dribbled TLS handshake held its worker for {held:?} past a 1s handshake deadline");
    }

    #[test]
    fn one_source_cannot_take_every_worker() {
        let options = ServerOptions { max_connections_per_source: 2, shutdown_drain: Duration::from_millis(200), ..Default::default() };
        let (addr, stop, server) = serve_with(options, None);
        let request = b"GET / HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n";
        let answer = |stream: &mut TcpStream| {
            let _ = stream.write_all(request);
            let mut reply = String::new();
            let _ = stream.set_read_timeout(Some(Duration::from_secs(3)));
            let _ = stream.read_to_string(&mut reply);
            reply
        };
        // Two idle connections hold both slots of this source.
        let idle: Vec<TcpStream> = (0..2).map(|_| TcpStream::connect(addr).unwrap()).collect();
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(answer(&mut TcpStream::connect(addr).unwrap()), "", "a third connection from the same source was served");
        drop(idle);
        let deadline = Instant::now() + Duration::from_secs(5);
        let mut served = String::new();
        while Instant::now() < deadline && !served.starts_with("HTTP/1.1 401") {
            std::thread::sleep(Duration::from_millis(100));
            served = answer(&mut TcpStream::connect(addr).unwrap());
        }
        stop.trigger();
        server.join().unwrap();
        assert!(served.starts_with("HTTP/1.1 401"), "released slots were not reusable");
        let limiter = Arc::new(SourceLimiter::default());
        let mapped: std::net::IpAddr = "::ffff:192.0.2.7".parse().unwrap();
        let plain: std::net::IpAddr = "192.0.2.7".parse().unwrap();
        let held = limiter.enter(mapped, 1).unwrap();
        assert!(limiter.enter(plain, 1).is_none(), "an IPv4-mapped peer was counted separately from its IPv4 address");
        drop(held);
        assert!(limiter.enter(plain, 1).is_some());
        assert!(limiter.counts.lock().unwrap().is_empty(), "released sources are forgotten");
    }

    #[test]
    fn header_field_count_is_bounded() {
        let options = ServerOptions { max_header_count: 8, shutdown_drain: Duration::from_millis(200), ..Default::default() };
        let (addr, stop, server) = serve_with(options, None);
        let exchange = |fields: usize| {
            let mut stream = TcpStream::connect(addr).unwrap();
            let mut request = String::from("GET / HTTP/1.1\r\nHost: t\r\nConnection: close\r\n");
            for i in 0..fields {
                request.push_str(&format!("X-Field-{i}: v\r\n"));
            }
            request.push_str("\r\n");
            stream.write_all(request.as_bytes()).unwrap();
            let mut reply = String::new();
            let _ = stream.read_to_string(&mut reply);
            reply
        };
        let (within, beyond) = (exchange(6), exchange(7));
        stop.trigger();
        server.join().unwrap();
        assert!(within.starts_with("HTTP/1.1 401"), "eight header fields were refused");
        assert!(beyond.starts_with("HTTP/1.1 431"), "nine header fields were accepted");
    }

    #[test]
    fn serves_routes_over_plain_http() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let mut router = Router::new();
        router.handle_fn("GET /api/v1/items/{id}", |r| Response::json(200, &serde_json::json!({"id": r.path_value("id"), "q": r.query().get("q")})));
        router.handle_fn("POST /api/v1/echo", |r| {
            r.limit_body(8);
            match r.read_body() {
                Ok(body) => Response::bytes(200, "text/plain", body),
                Err(err) => Response::error(&err.to_string(), 413),
            }
        });
        let router = Arc::new(router);
        let handler: Handler = Arc::new(move |r: &mut Request<'_>| router.dispatch(r));
        let stop = Shutdown::new();
        let stop_server = stop.clone();
        let server = std::thread::spawn(move || {
            serve(listener, None, handler, ServerOptions { shutdown_drain: Duration::from_millis(200), ..Default::default() }, &stop_server).unwrap()
        });

        let exchange = |raw: &str| {
            let mut stream = TcpStream::connect(addr).unwrap();
            stream.write_all(raw.as_bytes()).unwrap();
            let mut out = String::new();
            stream.read_to_string(&mut out).unwrap();
            out
        };
        let get = exchange("GET /api/v1/items/a%2Fb?q=x+y HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n");
        assert!(get.starts_with("HTTP/1.1 200 OK"), "unexpected GET status line ({} bytes)", get.len());
        assert!(get.ends_with("{\"id\":\"a/b\",\"q\":\"x y\"}\n"), "unexpected GET body ({} bytes)", get.len());
        let wrong_method = exchange("DELETE /api/v1/items/1 HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n");
        assert!(
            wrong_method.starts_with("HTTP/1.1 405") && wrong_method.contains("Allow: GET, HEAD"),
            "unexpected 405 response ({} bytes)",
            wrong_method.len()
        );
        let chunked =
            exchange("POST /api/v1/echo HTTP/1.1\r\nHost: t\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n3\r\nabc\r\n2\r\nde\r\n0\r\n\r\n");
        assert!(chunked.ends_with("\r\n\r\nabcde"), "unexpected chunked echo ({} bytes)", chunked.len());
        let too_large = exchange("POST /api/v1/echo HTTP/1.1\r\nHost: t\r\nContent-Length: 12\r\nConnection: close\r\n\r\n0123456789ab");
        assert!(
            too_large.starts_with("HTTP/1.1 413") && too_large.contains(crate::httpd::BODY_TOO_LARGE),
            "unexpected 413 response ({} bytes)",
            too_large.len()
        );
        // An oversized announced body with Expect: 100-continue is refused without
        // ever inviting it (found by the GitHub lab's hostile-HTTP test).
        let expect = exchange("POST /api/v1/echo HTTP/1.1\r\nHost: t\r\nContent-Length: 104857600\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n");
        assert!(expect.starts_with("HTTP/1.1 413"), "oversized Expect request was not refused with 413 ({} bytes)", expect.len());
        assert!(!expect.contains("100 Continue"), "the server invited an oversized body");
        assert!(exchange("GET /nope HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n").starts_with("HTTP/1.1 404"));
        for headers in [
            "Transfer-Encoding: chunked\r\nContent-Length: 0",
            "Transfer-Encoding: chunked\r\nTransfer-Encoding: chunked",
            "X-Bad\x0bName: value",
            "Content-Length: 9223372036854775808",
            "Content-Length: 18446744073709551615",
        ] {
            assert!(exchange(&format!("POST /api/v1/echo HTTP/1.1\r\nHost: t\r\n{headers}\r\nConnection: close\r\n\r\n")).starts_with("HTTP/1.1 400"));
        }
        stop.trigger();
        server.join().unwrap();
    }
}

/// In-memory access to the production parser, body decoder and response writer.
/// The feature is enabled only by the separate cargo-fuzz workspace.
#[cfg(feature = "fuzzing")]
pub fn fuzz_http_connection(data: &[u8]) {
    struct MemoryStream(io::Cursor<Vec<u8>>);
    impl Read for MemoryStream {
        fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
            self.0.read(buf)
        }
    }
    impl Write for MemoryStream {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            Ok(buf.len())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
    impl Stream for MemoryStream {
        fn set_read_deadline(&mut self, _: Option<Instant>) {}
        fn close_write(&mut self) {}
    }
    if data.len() > 64 * 1024 {
        return;
    }
    let handler: Handler = Arc::new(|request| {
        assert!(request.content_length >= 0 || request.headers.get("Transfer-Encoding").eq_ignore_ascii_case("chunked"));
        request.limit_body(64 * 1024);
        let _ = request.query();
        let _ = request.cookie("session");
        match request.read_body() {
            Ok(body) => Response::bytes(200, "application/octet-stream", body),
            Err(_) => Response::error("invalid body", 400),
        }
    });
    let options = ServerOptions { max_header_bytes: 4096, ..Default::default() };
    serve_connection(Box::new(MemoryStream(io::Cursor::new(data.to_vec()))), "127.0.0.1:1", "127.0.0.1:443", &handler, &options);
}

#[cfg(all(test, feature = "fuzzing"))]
#[test]
fn fuzz_harness_rejects_overflowing_content_length() {
    fuzz_http_connection(b"POST / HTTP/1.1\r\nHost: router\r\nContent-Length: 18446744073709551615\r\nConnection: close\r\n\r\n");
}
