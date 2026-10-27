//! Streaming multipart/form-data: parts are read in order without buffering
//! the whole body, so release archives go straight to disk.

use std::collections::HashMap;
use std::io::{self, Read};

use super::{media_type, Headers, Request};

pub struct MultipartReader<'b, 'r> {
    body: &'b mut dyn Read,
    delimiter: Vec<u8>,
    buf: Vec<u8>,
    eof: bool,
    finished: bool,
    started: bool,
    part_done: bool,
    _marker: std::marker::PhantomData<&'r ()>,
}

/// One part's headers; its content is read through `MultipartReader::read_part`.
#[derive(Clone, Debug, Default)]
pub struct Part {
    pub headers: Headers,
    pub name: String,
    pub filename: String,
}

impl<'b, 'r> MultipartReader<'b, 'r> {
    /// `Request.MultipartReader`: Err for a non-multipart request.
    pub fn new(request: &'b mut Request<'r>) -> Result<MultipartReader<'b, 'r>, String> {
        let (media, params) = media_type(request.headers.get("Content-Type")).map_err(|_| "request Content-Type isn't multipart/form-data".to_string())?;
        if media != "multipart/form-data" {
            return Err("request Content-Type isn't multipart/form-data".into());
        }
        let boundary = params.into_iter().find(|(k, _)| k == "boundary").map(|(_, v)| v).filter(|b| !b.is_empty()).ok_or("no multipart boundary param in Content-Type")?;
        Ok(MultipartReader { body: &mut request.body, delimiter: format!("\r\n--{}", boundary).into_bytes(), buf: Vec::new(), eof: false, finished: false, started: false, part_done: false, _marker: Default::default() })
    }

    fn fill(&mut self) -> io::Result<bool> {
        if self.eof {
            return Ok(false);
        }
        let mut chunk = [0u8; 16 * 1024];
        let n = self.body.read(&mut chunk)?;
        if n == 0 {
            self.eof = true;
            return Ok(false);
        }
        self.buf.extend_from_slice(&chunk[..n]);
        Ok(true)
    }

    fn find(&self, needle: &[u8], from: usize) -> Option<usize> {
        self.buf.get(from..)?.windows(needle.len()).position(|w| w == needle).map(|p| p + from)
    }

    fn read_line(&mut self) -> io::Result<Vec<u8>> {
        loop {
            if let Some(pos) = self.find(b"\n", 0) {
                let mut line: Vec<u8> = self.buf.drain(..=pos).collect();
                line.pop();
                if line.last() == Some(&b'\r') {
                    line.pop();
                }
                return Ok(line);
            }
            if self.buf.len() > 16 * 1024 {
                return Err(io::Error::new(io::ErrorKind::InvalidData, "multipart: header line too long"));
            }
            if !self.fill()? {
                return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "multipart: NextPart: EOF"));
            }
        }
    }

    /// The next part's headers, or None after the closing delimiter.
    pub fn next_part(&mut self) -> io::Result<Option<Part>> {
        if self.finished {
            return Ok(None);
        }
        if !self.started {
            // Skip the preamble up to the first "--boundary" line.
            let first = self.delimiter[2..].to_vec();
            loop {
                let line = self.read_line()?;
                if line == first {
                    break;
                }
                if line.starts_with(&first) && line[first.len()..].starts_with(b"--") {
                    self.finished = true;
                    return Ok(None);
                }
            }
            self.started = true;
        } else {
            // Discard any unread content of the previous part.
            let mut sink = [0u8; 8192];
            while self.read_part(&mut sink)? > 0 {}
            if self.finished {
                return Ok(None);
            }
        }
        self.part_done = false;
        let mut headers = Headers::default();
        let mut total = 0;
        loop {
            let line = self.read_line()?;
            if line.is_empty() {
                break;
            }
            total += line.len();
            if total > 10 << 10 {
                return Err(io::Error::new(io::ErrorKind::InvalidData, "multipart: message too large"));
            }
            let text = String::from_utf8_lossy(&line).into_owned();
            let (k, v) = text.split_once(':').ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "malformed MIME header"))?;
            headers.add(k.trim(), v.trim().to_string());
        }
        let disposition = headers.get("Content-Disposition").to_string();
        let params = media_type(&format!("x/{}", disposition.split_once(';').map(|(_, p)| format!("x;{}", p)).unwrap_or_default())).map(|(_, p)| p).unwrap_or_default();
        let get = |key: &str| params.iter().find(|(k, _)| k == key).map(|(_, v)| v.clone()).unwrap_or_default();
        // Like Go's Part.FileName, only the base name is kept.
        let filename = get("filename").rsplit(['/', '\\']).next().unwrap_or("").to_string();
        Ok(Some(Part { name: get("name"), filename, headers }))
    }

    /// Reads content of the current part; 0 at its end.
    pub fn read_part(&mut self, out: &mut [u8]) -> io::Result<usize> {
        if self.finished || self.part_done || out.is_empty() {
            return Ok(0);
        }
        loop {
            if let Some(pos) = self.find(&self.delimiter.clone(), 0) {
                if pos > 0 {
                    let n = pos.min(out.len());
                    out[..n].copy_from_slice(&self.buf[..n]);
                    self.buf.drain(..n);
                    return Ok(n);
                }
                // At the delimiter: need the two bytes after it.
                while self.buf.len() < self.delimiter.len() + 2 {
                    if !self.fill()? {
                        return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "multipart: unexpected EOF"));
                    }
                }
                let after = &self.buf[self.delimiter.len()..self.delimiter.len() + 2];
                if after == b"--" {
                    self.finished = true;
                    self.buf.clear();
                } else {
                    self.buf.drain(..self.delimiter.len());
                    // Rest of the delimiter line (transport padding, CRLF).
                    self.read_line()?;
                }
                self.part_done = true;
                return Ok(0);
            }
            // Everything except a possible delimiter prefix is content.
            let safe = self.buf.len().saturating_sub(self.delimiter.len() - 1);
            if safe > 0 {
                let n = safe.min(out.len());
                out[..n].copy_from_slice(&self.buf[..n]);
                self.buf.drain(..n);
                return Ok(n);
            }
            if !self.fill()? {
                return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "multipart: unexpected EOF"));
            }
        }
    }
}

/// `ParseMultipartForm` held in memory: values and file contents by name.
#[derive(Debug, Default)]
pub struct Form {
    pub values: HashMap<String, Vec<String>>,
    pub files: HashMap<String, Vec<(String, Vec<u8>)>>,
}

impl Form {
    pub fn value(&self, name: &str) -> String {
        self.values.get(name).and_then(|v| v.first()).cloned().unwrap_or_default()
    }

    pub fn file(&self, name: &str) -> Option<&[u8]> {
        self.files.get(name).and_then(|v| v.first()).map(|(_, data)| data.as_slice())
    }
}

/// Reads the whole form, failing once more than `max_bytes` of part content
/// has been read.
pub fn read_form(request: &mut Request<'_>, max_bytes: u64) -> Result<Form, String> {
    let mut reader = MultipartReader::new(request)?;
    let mut form = Form::default();
    let mut total = 0u64;
    while let Some(part) = reader.next_part().map_err(|e| e.to_string())? {
        let mut data = Vec::new();
        let mut buf = [0u8; 16 * 1024];
        loop {
            let n = reader.read_part(&mut buf).map_err(|e| e.to_string())?;
            if n == 0 {
                break;
            }
            total += n as u64;
            if total > max_bytes {
                return Err("multipart: message too large".into());
            }
            data.extend_from_slice(&buf[..n]);
        }
        if part.filename.is_empty() && !part.headers.has("Content-Type") {
            form.values.entry(part.name).or_default().push(String::from_utf8_lossy(&data).into_owned());
        } else {
            form.files.entry(part.name).or_default().push((part.filename, data));
        }
    }
    Ok(form)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_fields_and_files_across_small_reads() {
        let body = b"preamble\r\n--XyZ\r\nContent-Disposition: form-data; name=\"current_password\"\r\n\r\nsecret\r\n--XyZ\r\nContent-Disposition: form-data; name=\"backup\"; filename=\"C:\\\\tmp\\\\x.mrbak\"\r\nContent-Type: application/octet-stream\r\n\r\n\x00\x01\r\n--Xy\x02\r\n--XyZ--\r\nepilogue".to_vec();
        let length = body.len() as u64;
        let mut cursor = std::io::Cursor::new(body);
        let mut request = Request::synthetic("POST", "/upload", &mut cursor, length);
        request.headers.set("Content-Type", "multipart/form-data; boundary=XyZ");
        let form = read_form(&mut request, 1 << 20).unwrap();
        assert_eq!(form.value("current_password"), "secret", "{form:?}");
        assert!(form.files.contains_key("backup"), "{form:?}");
        assert_eq!(form.file("backup").unwrap(), b"\x00\x01\r\n--Xy\x02");
        assert_eq!(form.files["backup"][0].0, "x.mrbak");
    }
}
