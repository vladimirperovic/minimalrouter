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
    fn set_read_timeout(&mut self, timeout: Option<Duration>) -> io::Result<()>;
    fn send_continue(&mut self) -> io::Result<()>;
}

impl BodySource for io::Cursor<Vec<u8>> {
    fn set_read_timeout(&mut self, _: Option<Duration>) -> io::Result<()> {
        Ok(())
    }
    fn send_continue(&mut self) -> io::Result<()> {
        Ok(())
    }
}

trait Stream: Read + Write + Send {
    fn set_read_timeout(&self, timeout: Option<Duration>) -> io::Result<()>;
    fn set_write_timeout(&self, timeout: Option<Duration>) -> io::Result<()>;
    /// Sends FIN (after TLS close_notify) without discarding unread input.
    fn close_write(&mut self);
}

impl Stream for TcpStream {
    fn set_read_timeout(&self, timeout: Option<Duration>) -> io::Result<()> {
        TcpStream::set_read_timeout(self, timeout)
    }
    fn set_write_timeout(&self, timeout: Option<Duration>) -> io::Result<()> {
        TcpStream::set_write_timeout(self, timeout)
    }
    fn close_write(&mut self) {
        let _ = self.shutdown(std::net::Shutdown::Write);
    }
}

impl Stream for rustls::StreamOwned<rustls::ServerConnection, TcpStream> {
    fn set_read_timeout(&self, timeout: Option<Duration>) -> io::Result<()> {
        self.sock.set_read_timeout(timeout)
    }
    fn set_write_timeout(&self, timeout: Option<Duration>) -> io::Result<()> {
        self.sock.set_write_timeout(timeout)
    }
    fn close_write(&mut self) {
        self.conn.send_close_notify();
        let _ = self.flush();
        let _ = self.sock.shutdown(std::net::Shutdown::Write);
    }
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
    fn set_read_timeout(&mut self, timeout: Option<Duration>) -> io::Result<()> {
        self.reader.get_ref().set_read_timeout(timeout)
    }
    fn send_continue(&mut self) -> io::Result<()> {
        let stream = self.reader.get_mut();
        stream.write_all(b"HTTP/1.1 100 Continue\r\n\r\n")?;
        stream.flush()
    }
}

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
            while !read_line_from(source, 4096)?.is_empty() {}
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
    pub max_header_bytes: usize,
    pub max_connections: usize,
    pub shutdown_drain: Duration,
}

impl Default for ServerOptions {
    fn default() -> Self {
        ServerOptions {
            read_header_timeout: Duration::from_secs(5),
            read_timeout: Duration::from_secs(10),
            idle_timeout: Duration::from_secs(15),
            write_timeout: None,
            max_header_bytes: 1 << 20,
            max_connections: 128,
            shutdown_drain: Duration::from_secs(8),
        }
    }
}

/// Serves until `shutdown` starts, then waits up to `shutdown_drain` for
/// in-flight connections, like `http.Server.Shutdown`.
pub fn serve(listener: TcpListener, tls: Option<Arc<rustls::ServerConfig>>, handler: Handler, options: ServerOptions, shutdown: &Shutdown) -> io::Result<()> {
    let acceptor = Acceptor::new(listener, shutdown)?;
    let active = Arc::new(InFlight::default());
    while let Some((stream, peer)) = acceptor.next(shutdown)? {
        if active.len() >= options.max_connections {
            drop(stream);
            continue;
        }
        let _ = stream.set_nodelay(true);
        let in_flight = active.enter();
        let (handler, options, tls) = (handler.clone(), options.clone(), tls.clone());
        std::thread::spawn(move || {
            let local = stream.local_addr().map(|a| a.to_string()).unwrap_or_default();
            let remote = peer.to_string();
            let boxed: Option<Box<dyn Stream>> = match tls {
                Some(config) => rustls::ServerConnection::new(config).ok().map(|conn| Box::new(rustls::StreamOwned::new(conn, stream)) as Box<dyn Stream>),
                None => Some(Box::new(stream)),
            };
            if let Some(boxed) = boxed {
                serve_connection(boxed, &remote, &local, &handler, &options);
            }
            drop(in_flight);
        });
    }
    active.wait_idle(options.shutdown_drain);
    Ok(())
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
    reader.get_ref().set_read_timeout(Some(wait)).map_err(|_| HeadError::Closed)?;
    if reader.fill_buf().map(|b| b.is_empty()).unwrap_or(true) {
        return Err(HeadError::Closed);
    }
    let deadline = Instant::now() + options.read_header_timeout;
    let mut total = 0usize;
    let mut lines: Vec<String> = Vec::new();
    loop {
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(HeadError::Closed);
        }
        reader.get_ref().set_read_timeout(Some(remaining)).map_err(|_| HeadError::Closed)?;
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
        if name.is_empty() || name.contains([' ', '\t']) {
            return Err(HeadError::BadRequest("malformed MIME header line"));
        }
        headers.add(&canonical_key(name), value.trim_matches([' ', '\t']).to_string());
    }
    let te = headers.get("Transfer-Encoding").to_ascii_lowercase();
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
    let _ = conn.reader.get_ref().set_write_timeout(options.write_timeout);
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
                value => match value.parse::<u64>() {
                    Ok(n) => n as i64,
                    Err(_) => return simple_error(conn.reader.get_mut(), 400, "Bad Request: bad Content-Length"),
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
    let mut sink = [0u8; 4096];
    while Instant::now() < deadline {
        let _ = conn.reader.get_ref().set_read_timeout(Some(deadline.saturating_duration_since(Instant::now()).max(Duration::from_millis(1))));
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
    let text = std::str::from_utf8(body).is_ok() && !body.iter().any(|&b| b < 0x09 || (0x0e..0x20).contains(&b) && b != 0x1b);
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
        stop.trigger();
        server.join().unwrap();
    }
}
