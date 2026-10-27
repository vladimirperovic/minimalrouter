//! `strings` / `unicode` helpers whose behaviour differs from Rust's defaults.

/// `strings.ToLower`: simple per-rune mapping with no contextual rules.
pub fn to_lower(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_lowercase();
    }
    value.chars().map(|c| c.to_lowercase().next().unwrap_or(c)).collect()
}

/// `strings.ToUpper`.
pub fn to_upper(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_uppercase();
    }
    value.chars().map(|c| c.to_uppercase().next().unwrap_or(c)).collect()
}

/// `strings.TrimSpace` (Unicode White_Space, identical to `str::trim`).
pub fn trim_space(value: &str) -> &str {
    value.trim()
}

/// `strings.IndexFunc(value, unicode.IsControl) >= 0`.
pub fn has_control(value: &str) -> bool {
    value.chars().any(char::is_control)
}

/// `strings.Fields`.
pub fn fields(value: &str) -> Vec<&str> {
    value.split_whitespace().collect()
}

/// `utf8.RuneCountInString`.
pub fn rune_count(value: &str) -> usize {
    value.chars().count()
}

/// `strconv.Quote`-style quoting used in Go `%q` formatting for printable text.
pub fn quote(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    out.push('"');
    for c in value.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\x07' => out.push_str("\\a"),
            '\x08' => out.push_str("\\b"),
            '\x0c' => out.push_str("\\f"),
            '\x0b' => out.push_str("\\v"),
            c if (c as u32) < 0x20 || c as u32 == 0x7f => out.push_str(&format!("\\x{:02x}", c as u32)),
            c if c.is_control() => {
                if (c as u32) < 0x10000 {
                    out.push_str(&format!("\\u{:04x}", c as u32));
                } else {
                    out.push_str(&format!("\\U{:08x}", c as u32));
                }
            }
            c => out.push(c),
        }
    }
    out.push('"');
    out
}
