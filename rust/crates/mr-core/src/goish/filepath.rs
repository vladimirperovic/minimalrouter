//! `path/filepath` with Unix semantics, which is what the appliance runs.
//! Manifest paths always use `/`, so the same rules apply on a developer host.

/// Go `filepath.Clean`: lexical processing only, never touches the disk.
pub fn clean(path: &str) -> String {
    if path.is_empty() {
        return ".".into();
    }
    let bytes = path.as_bytes();
    let rooted = bytes[0] == b'/';
    let n = bytes.len();
    // Go's lazybuf: `w` is the logical length; bytes past it are scratch.
    let mut buf: Vec<u8> = vec![0; n + 1];
    let mut w = 0usize;
    let append = |buf: &mut Vec<u8>, w: &mut usize, c: u8| {
        buf[*w] = c;
        *w += 1;
    };
    let (mut r, mut dotdot) = (0usize, 0usize);
    if rooted {
        append(&mut buf, &mut w, b'/');
        r = 1;
        dotdot = 1;
    }
    while r < n {
        if bytes[r] == b'/' {
            r += 1;
        } else if bytes[r] == b'.' && (r + 1 == n || bytes[r + 1] == b'/') {
            r += 1;
        } else if bytes[r] == b'.' && bytes[r + 1] == b'.' && (r + 2 == n || bytes[r + 2] == b'/') {
            r += 2;
            if w > dotdot {
                w -= 1;
                while w > dotdot && buf[w] != b'/' {
                    w -= 1;
                }
            } else if !rooted {
                if w > 0 {
                    append(&mut buf, &mut w, b'/');
                }
                append(&mut buf, &mut w, b'.');
                append(&mut buf, &mut w, b'.');
                dotdot = w;
            }
        } else {
            if (rooted && w != 1) || (!rooted && w != 0) {
                append(&mut buf, &mut w, b'/');
            }
            while r < n && bytes[r] != b'/' {
                append(&mut buf, &mut w, bytes[r]);
                r += 1;
            }
        }
    }
    if w == 0 {
        return ".".into();
    }
    buf.truncate(w);
    String::from_utf8(buf).unwrap_or_else(|e| String::from_utf8_lossy(e.as_bytes()).into_owned())
}

pub fn is_abs(path: &str) -> bool {
    path.starts_with('/')
}

/// Go `filepath.IsLocal`: a relative path that stays within its directory.
pub fn is_local(path: &str) -> bool {
    if is_abs(path) || path.is_empty() {
        return false;
    }
    let path = if path.split('/').any(|part| part == "." || part == "..") { clean(path) } else { path.to_string() };
    !(path == ".." || path.starts_with("../"))
}

/// Go `filepath.Dir`.
pub fn dir(path: &str) -> String {
    match path.rfind('/') {
        Some(i) => clean(&path[..=i]),
        None => ".".into(),
    }
}

/// Go `filepath.Base`.
pub fn base(path: &str) -> String {
    if path.is_empty() {
        return ".".into();
    }
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return "/".into();
    }
    trimmed.rsplit('/').next().unwrap_or(trimmed).to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_go_clean() {
        for (input, want) in [
            ("", "."),
            ("abc/../../x", "../x"),
            ("/../a//b/./c/", "/a/b/c"),
            ("a/b/..", "a"),
            ("..", ".."),
            ("./", "."),
            ("bin/routerd-amd64", "bin/routerd-amd64"),
        ] {
            assert_eq!(clean(input), want, "{input}");
        }
        assert!(is_local("bin/x") && !is_local("../x") && !is_local("a/../../x") && !is_local("/x"));
        assert_eq!((dir("bin/x"), dir("x"), base("a/b/")), ("bin".to_string(), ".".to_string(), "b".to_string()));
    }
}
