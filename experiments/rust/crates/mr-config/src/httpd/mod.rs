//! A small synchronous HTTP/1.1 server with the net/http semantics the API
//! relies on: Go 1.22 ServeMux routing (methods, `{wildcards}`, 405 with
//! Allow), `http.Error` bodies, MaxBytesReader, per-request read deadlines,
//! streamed and chunked request bodies, and TLS with a live certificate.

mod multipart;
mod router;
mod server;

pub use multipart::{read_form, Form, MultipartReader, Part};
pub use router::{Handler, Router};
pub use server::{serve, BodySource, CertResolver, ServerOptions};

use serde::Serialize;
use std::io::{self, Read};
use std::time::{Duration, Instant};

/// Case-insensitive header list that keeps insertion order, like
/// `http.Header` with canonicalized keys.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Headers {
    items: Vec<(String, String)>,
}

/// textproto.CanonicalMIMEHeaderKey for ASCII token names.
pub fn canonical_key(name: &str) -> String {
    let mut upper = true;
    name.chars()
        .map(|c| {
            let out = if upper { c.to_ascii_uppercase() } else { c.to_ascii_lowercase() };
            upper = c == '-';
            out
        })
        .collect()
}

impl Headers {
    pub fn get(&self, name: &str) -> &str {
        self.items.iter().find(|(k, _)| k.eq_ignore_ascii_case(name)).map(|(_, v)| v.as_str()).unwrap_or("")
    }

    pub fn has(&self, name: &str) -> bool {
        self.items.iter().any(|(k, _)| k.eq_ignore_ascii_case(name))
    }

    pub fn values(&self, name: &str) -> Vec<&str> {
        self.items.iter().filter(|(k, _)| k.eq_ignore_ascii_case(name)).map(|(_, v)| v.as_str()).collect()
    }

    pub fn set(&mut self, name: &str, value: impl Into<String>) {
        self.del(name);
        self.items.push((canonical_key(name), value.into()));
    }

    pub fn add(&mut self, name: &str, value: impl Into<String>) {
        self.items.push((canonical_key(name), value.into()));
    }

    pub fn del(&mut self, name: &str) {
        self.items.retain(|(k, _)| !k.eq_ignore_ascii_case(name));
    }

    pub fn iter(&self) -> impl Iterator<Item = &(String, String)> {
        self.items.iter()
    }
}

/// Parsed `url.Values`; `get` returns the first value or "".
#[derive(Clone, Debug, Default)]
pub struct Query(Vec<(String, String)>);

impl Query {
    pub fn parse(raw: &str) -> Query {
        let mut out = Vec::new();
        for pair in raw.split('&') {
            // Go 1.17+ rejects semicolon separators: the pair is dropped.
            if pair.is_empty() || pair.contains(';') {
                continue;
            }
            let (k, v) = pair.split_once('=').unwrap_or((pair, ""));
            if let (Some(k), Some(v)) = (unescape(k, true), unescape(v, true)) {
                out.push((k, v));
            }
        }
        Query(out)
    }

    pub fn get(&self, key: &str) -> String {
        self.0.iter().find(|(k, _)| k == key).map(|(_, v)| v.clone()).unwrap_or_default()
    }

    pub fn has(&self, key: &str) -> bool {
        self.0.iter().any(|(k, _)| k == key)
    }
}

/// url.PathUnescape / QueryUnescape: None on a malformed escape.
pub fn unescape(value: &str, query: bool) -> Option<String> {
    let bytes = value.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'%' => {
                let hex = bytes.get(i + 1..i + 3)?;
                out.push(u8::from_str_radix(std::str::from_utf8(hex).ok()?, 16).ok()?);
                i += 3;
            }
            b'+' if query => {
                out.push(b' ');
                i += 1;
            }
            b => {
                out.push(b);
                i += 1;
            }
        }
    }
    Some(String::from_utf8_lossy(&out).into_owned())
}

pub const BODY_TOO_LARGE: &str = "http: request body too large";

/// The request body: Content-Length or chunked framing over the connection,
/// bounded by an optional MaxBytesReader limit and an absolute deadline.
pub struct Body<'a> {
    pub(crate) source: Option<&'a mut dyn server::BodySource>,
    pub(crate) remaining: u64,
    pub(crate) chunked: Option<server::ChunkState>,
    pub(crate) limit: Option<u64>,
    pub(crate) consumed: u64,
    pub(crate) deadline: Instant,
    pub(crate) continue_pending: bool,
    pub(crate) exceeded: bool,
}

impl Body<'_> {
    pub fn empty() -> Body<'static> {
        Body { source: None, remaining: 0, chunked: None, limit: None, consumed: 0, deadline: Instant::now(), continue_pending: false, exceeded: false }
    }

    fn read_framed(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let Some(source) = self.source.as_deref_mut() else { return Ok(0) };
        if self.continue_pending {
            self.continue_pending = false;
            source.send_continue()?;
        }
        let remaining = self.deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(io::Error::new(io::ErrorKind::TimedOut, "i/o timeout"));
        }
        source.set_read_timeout(Some(remaining))?;
        match &mut self.chunked {
            Some(state) => server::read_chunked(source, state, buf),
            None => {
                if self.remaining == 0 {
                    return Ok(0);
                }
                let want = buf.len().min(self.remaining.min(usize::MAX as u64) as usize);
                let n = source.read(&mut buf[..want])?;
                if n == 0 {
                    return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "unexpected EOF"));
                }
                self.remaining -= n as u64;
                Ok(n)
            }
        }
    }

    pub(crate) fn finished(&self) -> bool {
        match &self.chunked {
            Some(state) => state.done,
            None => self.remaining == 0,
        }
    }
}

impl Read for Body<'_> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if buf.is_empty() {
            return Ok(0);
        }
        if let Some(limit) = self.limit {
            if self.consumed >= limit {
                // One extra byte proves the body is larger than allowed.
                let mut probe = [0u8; 1];
                if self.read_framed(&mut probe)? == 0 {
                    return Ok(0);
                }
                self.exceeded = true;
                return Err(io::Error::other(BODY_TOO_LARGE));
            }
            let allowed = (limit - self.consumed).min(buf.len() as u64) as usize;
            let n = self.read_framed(&mut buf[..allowed])?;
            self.consumed += n as u64;
            return Ok(n);
        }
        let n = self.read_framed(buf)?;
        self.consumed += n as u64;
        Ok(n)
    }
}

/// An incoming request. `path` is percent-decoded like `URL.Path`.
pub struct Request<'a> {
    pub method: String,
    pub path: String,
    pub raw_path: String,
    pub raw_query: String,
    pub proto: String,
    pub headers: Headers,
    pub host: String,
    pub remote_addr: String,
    pub local_addr: String,
    /// -1 when unknown (chunked), like `Request.ContentLength`.
    pub content_length: i64,
    pub params: Vec<(String, String)>,
    pub session: Option<crate::auth::Session>,
    pub body: Body<'a>,
}

impl<'a> Request<'a> {
    /// A request with no connection, for tests and internal dispatch.
    pub fn synthetic(method: &str, target: &str, body: &'a mut dyn server::BodySource, length: u64) -> Request<'a> {
        let (raw_path, raw_query) = target.split_once('?').unwrap_or((target, ""));
        Request {
            method: method.into(),
            path: unescape(raw_path, false).unwrap_or_default(),
            raw_path: raw_path.into(),
            raw_query: raw_query.into(),
            proto: "HTTP/1.1".into(),
            headers: Headers::default(),
            host: "localhost".into(),
            remote_addr: "127.0.0.1:50000".into(),
            local_addr: "127.0.0.1:443".into(),
            content_length: length as i64,
            params: Vec::new(),
            session: None,
            body: Body { source: Some(body), remaining: length, chunked: None, limit: None, consumed: 0, deadline: Instant::now() + Duration::from_secs(10), continue_pending: false, exceeded: false },
        }
    }

    pub fn path_value(&self, name: &str) -> String {
        self.params.iter().find(|(k, _)| k == name).map(|(_, v)| v.clone()).unwrap_or_default()
    }

    pub fn query(&self) -> Query {
        Query::parse(&self.raw_query)
    }

    /// http.Request.Cookie: the first cookie with this name.
    pub fn cookie(&self, name: &str) -> Option<String> {
        for header in self.headers.values("Cookie") {
            for part in header.split(';') {
                let part = part.trim();
                let (k, v) = part.split_once('=').unwrap_or((part, ""));
                if k == name {
                    let v = v.strip_prefix('"').and_then(|v| v.strip_suffix('"')).unwrap_or(v);
                    return Some(v.to_string());
                }
            }
        }
        None
    }

    /// http.MaxBytesReader for the remainder of the body.
    pub fn limit_body(&mut self, limit: u64) {
        self.body.limit = Some(self.body.consumed + limit);
    }

    /// ResponseController.SetReadDeadline for an authenticated upload.
    pub fn set_read_deadline(&mut self, deadline: Instant) {
        self.body.deadline = deadline;
    }

    /// io.ReadAll of the (possibly limited) body.
    pub fn read_body(&mut self) -> io::Result<Vec<u8>> {
        let mut data = Vec::new();
        self.body.read_to_end(&mut data)?;
        Ok(data)
    }

    pub fn has_body(&self) -> bool {
        self.content_length != 0
    }
}

/// What a handler returns. `Stream` bodies are sent chunked.
pub enum ResponseBody {
    Bytes(Vec<u8>),
    Stream(Box<dyn Read + Send>),
}

pub struct Response {
    pub status: u16,
    pub headers: Headers,
    pub body: ResponseBody,
}

impl Response {
    pub fn new(status: u16) -> Response {
        Response { status, headers: Headers::default(), body: ResponseBody::Bytes(Vec::new()) }
    }

    pub fn bytes(status: u16, content_type: &str, body: Vec<u8>) -> Response {
        let mut response = Response::new(status);
        if !content_type.is_empty() {
            response.headers.set("Content-Type", content_type);
        }
        response.body = ResponseBody::Bytes(body);
        response
    }

    /// `json.NewEncoder(w).Encode(v)` after setting application/json: Go's
    /// HTML escaping and a trailing newline.
    pub fn json<T: Serialize + ?Sized>(status: u16, value: &T) -> Response {
        let mut body = crate::goish::json::marshal(value).unwrap_or_else(|_| b"null".to_vec());
        body.push(b'\n');
        Response::bytes(status, "application/json", body)
    }

    /// `http.Error`.
    pub fn error(message: &str, status: u16) -> Response {
        let mut response = Response::bytes(status, "text/plain; charset=utf-8", format!("{}\n", message).into_bytes());
        response.headers.set("X-Content-Type-Options", "nosniff");
        response
    }

    pub fn with_header(mut self, name: &str, value: impl Into<String>) -> Response {
        self.headers.set(name, value);
        self
    }

    pub fn body_bytes(&self) -> &[u8] {
        match &self.body {
            ResponseBody::Bytes(b) => b,
            ResponseBody::Stream(_) => &[],
        }
    }
}

/// net/http status text for the status line.
pub fn status_text(status: u16) -> &'static str {
    match status {
        100 => "Continue",
        200 => "OK",
        201 => "Created",
        202 => "Accepted",
        204 => "No Content",
        206 => "Partial Content",
        301 => "Moved Permanently",
        302 => "Found",
        304 => "Not Modified",
        307 => "Temporary Redirect",
        308 => "Permanent Redirect",
        400 => "Bad Request",
        401 => "Unauthorized",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        408 => "Request Timeout",
        409 => "Conflict",
        410 => "Gone",
        411 => "Length Required",
        412 => "Precondition Failed",
        413 => "Request Entity Too Large",
        415 => "Unsupported Media Type",
        422 => "Unprocessable Entity",
        423 => "Locked",
        428 => "Precondition Required",
        429 => "Too Many Requests",
        431 => "Request Header Fields Too Large",
        500 => "Internal Server Error",
        501 => "Not Implemented",
        502 => "Bad Gateway",
        503 => "Service Unavailable",
        504 => "Gateway Timeout",
        507 => "Insufficient Storage",
        _ => "",
    }
}

/// mime.ParseMediaType's media type, lowercased; Err on a malformed value.
pub fn media_type(value: &str) -> Result<(String, Vec<(String, String)>), ()> {
    let mut parts = value.split(';');
    let media = parts.next().unwrap_or("").trim().to_ascii_lowercase();
    let token = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphanumeric() || b"!#$%&'*+-.^_`|~".contains(&b));
    let (kind, sub) = media.split_once('/').ok_or(())?;
    if !token(kind) || !token(sub) {
        return Err(());
    }
    let mut params = Vec::new();
    for param in parts {
        let param = param.trim();
        if param.is_empty() {
            continue;
        }
        let (k, v) = param.split_once('=').ok_or(())?;
        let k = k.trim().to_ascii_lowercase();
        if !token(&k) {
            return Err(());
        }
        let v = v.trim();
        let v = v.strip_prefix('"').and_then(|v| v.strip_suffix('"')).map(|v| v.replace("\\\"", "\"")).unwrap_or_else(|| v.to_string());
        params.push((k, v));
    }
    Ok((media, params))
}
