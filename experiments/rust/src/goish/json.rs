//! `encoding/json` compatibility.
//!
//! Canonical configuration is persisted and compared as JSON (`EqualSection`,
//! request hashes, audit diffs), so the Rust encoder must emit the same bytes as
//! Go: nil slices encode as `null` while empty slices encode as `[]`, `<`, `>`
//! and `&` are escaped, and floats use Go's shortest formatting.

use serde::de::DeserializeOwned;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::ser::{CompactFormatter, Formatter, PrettyFormatter};
use std::io;
use std::ops::{Deref, DerefMut};

/// A Go slice: `null` (nil) and `[]` (empty) are distinct encodings.
///
/// Appending to a nil list makes it non-nil, exactly as `append` does in Go.
#[derive(Clone, Debug, Eq, Hash)]
pub struct List<T> {
    items: Vec<T>,
    nil: bool,
}

impl<T> List<T> {
    pub const fn nil() -> Self {
        List { items: Vec::new(), nil: true }
    }

    pub fn empty() -> Self {
        List { items: Vec::new(), nil: false }
    }

    pub fn is_nil(&self) -> bool {
        self.nil && self.items.is_empty()
    }

    pub fn into_vec(self) -> Vec<T> {
        self.items
    }

    /// Go's `len(s) == 0`, for `omitempty`.
    pub fn is_empty(&self) -> bool {
        self.items.is_empty()
    }
}

impl<T> Default for List<T> {
    fn default() -> Self {
        List::nil()
    }
}

impl<T: PartialEq> PartialEq for List<T> {
    fn eq(&self, other: &Self) -> bool {
        self.is_nil() == other.is_nil() && self.items == other.items
    }
}

impl<T> Deref for List<T> {
    type Target = Vec<T>;
    fn deref(&self) -> &Vec<T> {
        &self.items
    }
}

impl<T> DerefMut for List<T> {
    fn deref_mut(&mut self) -> &mut Vec<T> {
        &mut self.items
    }
}

impl<T> From<Vec<T>> for List<T> {
    fn from(items: Vec<T>) -> Self {
        List { items, nil: false }
    }
}

impl<T> FromIterator<T> for List<T> {
    fn from_iter<I: IntoIterator<Item = T>>(iter: I) -> Self {
        List { items: iter.into_iter().collect(), nil: false }
    }
}

impl<'a, T> IntoIterator for &'a List<T> {
    type Item = &'a T;
    type IntoIter = std::slice::Iter<'a, T>;
    fn into_iter(self) -> Self::IntoIter {
        self.items.iter()
    }
}

impl<'a, T> IntoIterator for &'a mut List<T> {
    type Item = &'a mut T;
    type IntoIter = std::slice::IterMut<'a, T>;
    fn into_iter(self) -> Self::IntoIter {
        self.items.iter_mut()
    }
}

impl<T> IntoIterator for List<T> {
    type Item = T;
    type IntoIter = std::vec::IntoIter<T>;
    fn into_iter(self) -> Self::IntoIter {
        self.items.into_iter()
    }
}

impl<T: Serialize> Serialize for List<T> {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        if self.is_nil() {
            serializer.serialize_none()
        } else {
            self.items.serialize(serializer)
        }
    }
}

impl<'de, T: Deserialize<'de>> Deserialize<'de> for List<T> {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        Ok(match Option::<Vec<T>>::deserialize(deserializer)? {
            Some(items) => List { items, nil: false },
            None => List::nil(),
        })
    }
}

#[macro_export]
macro_rules! list {
    () => { $crate::goish::json::List::empty() };
    ($($x:expr),+ $(,)?) => { $crate::goish::json::List::from(vec![$($x),+]) };
}

/// Go's string escaping: HTML-sensitive characters and U+2028/U+2029 are
/// escaped in addition to what JSON requires.
struct GoFormatter<F> {
    inner: F,
}

impl<F: Formatter> Formatter for GoFormatter<F> {
    fn write_string_fragment<W: ?Sized + io::Write>(&mut self, writer: &mut W, fragment: &str) -> io::Result<()> {
        let mut start = 0;
        for (i, ch) in fragment.char_indices() {
            let escape = match ch {
                '<' => "\\u003c",
                '>' => "\\u003e",
                '&' => "\\u0026",
                '\u{2028}' => "\\u2028",
                '\u{2029}' => "\\u2029",
                _ => continue,
            };
            writer.write_all(fragment[start..i].as_bytes())?;
            writer.write_all(escape.as_bytes())?;
            start = i + ch.len_utf8();
        }
        writer.write_all(fragment[start..].as_bytes())
    }

    fn write_f64<W: ?Sized + io::Write>(&mut self, writer: &mut W, value: f64) -> io::Result<()> {
        writer.write_all(format_float(value, 64).as_bytes())
    }

    fn write_f32<W: ?Sized + io::Write>(&mut self, writer: &mut W, value: f32) -> io::Result<()> {
        writer.write_all(format_float(value as f64, 32).as_bytes())
    }

    // Delegate structural formatting (indentation) to the wrapped formatter.
    fn begin_array<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.begin_array(w)
    }
    fn end_array<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.end_array(w)
    }
    fn begin_array_value<W: ?Sized + io::Write>(&mut self, w: &mut W, first: bool) -> io::Result<()> {
        self.inner.begin_array_value(w, first)
    }
    fn end_array_value<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.end_array_value(w)
    }
    fn begin_object<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.begin_object(w)
    }
    fn end_object<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.end_object(w)
    }
    fn begin_object_key<W: ?Sized + io::Write>(&mut self, w: &mut W, first: bool) -> io::Result<()> {
        self.inner.begin_object_key(w, first)
    }
    fn end_object_key<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.end_object_key(w)
    }
    fn begin_object_value<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.begin_object_value(w)
    }
    fn end_object_value<W: ?Sized + io::Write>(&mut self, w: &mut W) -> io::Result<()> {
        self.inner.end_object_value(w)
    }
}

/// Go's float encoding: shortest round-trip digits, exponent form outside
/// [1e-6, 1e21), and `e-7` instead of `e-07`.
fn format_float(value: f64, bits: u32) -> String {
    let abs = value.abs();
    let exponent = abs != 0.0
        && ((bits == 64 && (abs < 1e-6 || abs >= 1e21))
            || (bits == 32 && ((abs as f32) < 1e-6 || (abs as f32) >= 1e21)));
    if !exponent {
        let text = if bits == 32 { format!("{}", value as f32) } else { format!("{}", value) };
        return text;
    }
    let text = if bits == 32 { format!("{:e}", value as f32) } else { format!("{:e}", value) };
    // Rust: 1e21 / 1.5e-7 -> Go: 1e+21 / 1.5e-07 then cleaned to 1.5e-7.
    let (mantissa, exp) = text.split_once('e').unwrap_or((&text, "0"));
    let (sign, digits) = match exp.strip_prefix('-') {
        Some(d) => ('-', d),
        None => ('+', exp),
    };
    let digits = if digits.len() < 2 && sign == '+' { format!("0{}", digits) } else { digits.to_string() };
    format!("{}e{}{}", mantissa, sign, digits)
}

/// `json.Marshal`.
pub fn marshal<T: Serialize + ?Sized>(value: &T) -> serde_json::Result<Vec<u8>> {
    let mut out = Vec::with_capacity(128);
    let mut ser = serde_json::Serializer::with_formatter(&mut out, GoFormatter { inner: CompactFormatter });
    value.serialize(&mut ser)?;
    Ok(out)
}

pub fn marshal_string<T: Serialize + ?Sized>(value: &T) -> serde_json::Result<String> {
    marshal(value).map(|bytes| String::from_utf8(bytes).expect("serde_json emits UTF-8"))
}

/// `json.MarshalIndent(v, "", indent)`.
pub fn marshal_indent<T: Serialize + ?Sized>(value: &T, indent: &str) -> serde_json::Result<Vec<u8>> {
    let mut out = Vec::with_capacity(256);
    let formatter = GoFormatter { inner: PrettyFormatter::with_indent(indent.as_bytes()) };
    let mut ser = serde_json::Serializer::with_formatter(&mut out, formatter);
    value.serialize(&mut ser)?;
    Ok(out)
}

/// `json.Unmarshal`, with Go's decoding rules (see [`super::decode`]).
pub fn unmarshal<T: DeserializeOwned>(data: &[u8]) -> serde_json::Result<T> {
    let value: serde_json::Value = serde_json::from_slice(data)?;
    from_value(&value)
}

/// Decodes an already parsed JSON value with Go's rules.
pub fn from_value<T: DeserializeOwned>(value: &serde_json::Value) -> serde_json::Result<T> {
    T::deserialize(super::decode::GoValue(value))
}

/// `bytes.Equal(json.Marshal(a), json.Marshal(b))` with both encodings required to succeed.
pub fn equal_json<A: Serialize + ?Sized, B: Serialize + ?Sized>(a: &A, b: &B) -> bool {
    match (marshal(a), marshal(b)) {
        (Ok(left), Ok(right)) => left == right,
        _ => false,
    }
}

/// serde `skip_serializing_if` helpers mirroring Go `omitempty`.
pub fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}
pub fn is_zero_i32(v: &i32) -> bool {
    *v == 0
}
pub fn is_zero_u64(v: &u64) -> bool {
    *v == 0
}
pub fn is_zero_f64(v: &f64) -> bool {
    *v == 0.0
}
pub fn is_false(v: &bool) -> bool {
    !*v
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Serialize, Deserialize, Default)]
    struct Sample {
        a: List<String>,
        #[serde(default, skip_serializing_if = "List::is_empty")]
        b: List<i32>,
        s: String,
        f: f64,
    }

    #[test]
    fn nil_and_empty_lists_encode_like_go() {
        let mut v = Sample { s: "<a&b>".into(), f: 100.0, ..Default::default() };
        assert_eq!(marshal_string(&v).unwrap(), "{\"a\":null,\"s\":\"\\u003ca\\u0026b\\u003e\",\"f\":100}");
        v.a = List::empty();
        v.b.push(1);
        assert_eq!(marshal_string(&v).unwrap(), "{\"a\":[],\"b\":[1],\"s\":\"\\u003ca\\u0026b\\u003e\",\"f\":100}");
        let back: Sample = unmarshal(br#"{"a":null,"s":"","f":0}"#).unwrap();
        assert!(back.a.is_nil());
        let back: Sample = unmarshal(br#"{"a":[],"s":"","f":0}"#).unwrap();
        assert!(!back.a.is_nil());
    }

    #[test]
    fn floats_match_go() {
        assert_eq!(format_float(93.5, 64), "93.5");
        assert_eq!(format_float(1e21, 64), "1e+21");
        assert_eq!(format_float(1.5e-7, 64), "1.5e-7");
        assert_eq!(format_float(0.0, 64), "0");
        assert_eq!(format_float(33.333333333333336, 64), "33.333333333333336");
    }
}
