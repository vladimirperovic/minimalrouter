//! `json.Unmarshal` decoding rules on top of serde.
//!
//! serde and Go disagree on a few inputs that matter for API requests and
//! stored state. This deserializer wraps a parsed `serde_json::Value` and
//! applies Go's rules instead:
//!
//! * `null` leaves a struct field untouched (it keeps its default), and a
//!   `null` array element or top-level scalar decodes as the zero value;
//! * a JSON array is never accepted where an object (Go struct) is expected;
//! * object keys match struct fields exactly first, then case-insensitively,
//!   and the last of several matching keys wins.

use serde::de::{self, DeserializeSeed, Deserializer, IntoDeserializer, MapAccess, SeqAccess, Visitor};
use serde_json::{Map, Value};

#[derive(Clone, Copy)]
pub struct GoValue<'a>(pub &'a Value);

fn type_error<E: de::Error>(value: &Value, target: &str) -> E {
    let kind = match value {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    };
    E::custom(format!("json: cannot unmarshal {} into Go value of type {}", kind, target))
}

/// Go's `strings.EqualFold`-based field matching, after an exact match.
fn canonical_key(key: &str, fields: &'static [&'static str]) -> Option<&'static str> {
    if let Some(exact) = fields.iter().find(|f| **f == key) {
        return Some(exact);
    }
    fields.iter().find(|f| f.len() == key.len() && f.chars().zip(key.chars()).all(|(a, b)| fold_eq(a, b))).copied()
}

fn fold_eq(a: char, b: char) -> bool {
    a == b || a.to_lowercase().eq(b.to_lowercase()) || (a == 'k' && b == '\u{212a}') || (a == 's' && b == '\u{17f}')
}

struct StructMap<'a> {
    entries: std::vec::IntoIter<(String, &'a Value)>,
    pending: Option<&'a Value>,
}

impl<'de> MapAccess<'de> for StructMap<'de> {
    type Error = serde_json::Error;

    fn next_key_seed<K: DeserializeSeed<'de>>(&mut self, seed: K) -> Result<Option<K::Value>, Self::Error> {
        match self.entries.next() {
            Some((key, value)) => {
                self.pending = Some(value);
                seed.deserialize(key.into_deserializer()).map(Some)
            }
            None => Ok(None),
        }
    }

    fn next_value_seed<V: DeserializeSeed<'de>>(&mut self, seed: V) -> Result<V::Value, Self::Error> {
        let value = self.pending.take().ok_or_else(|| de::Error::custom("value requested before key"))?;
        seed.deserialize(GoValue(value))
    }
}

fn struct_entries<'a>(map: &'a Map<String, Value>, fields: &'static [&'static str]) -> Vec<(String, &'a Value)> {
    let mut out: Vec<(String, &'a Value)> = Vec::with_capacity(map.len());
    for (key, value) in map {
        // Go leaves the field untouched on null.
        if value.is_null() {
            continue;
        }
        let name = canonical_key(key, fields).map(str::to_string).unwrap_or_else(|| key.clone());
        // Exact keys sort after their case-folded twins, so the exact
        // spelling wins, matching Go's preference for exact matches.
        if let Some(existing) = out.iter_mut().find(|(k, _)| *k == name) {
            if key == &name || canonical_key(&existing.0, fields).is_none() {
                existing.1 = value;
            }
            continue;
        }
        out.push((name, value));
    }
    out
}

struct ValueMap<'a> {
    entries: serde_json::map::Iter<'a>,
    pending: Option<&'a Value>,
}

impl<'de> MapAccess<'de> for ValueMap<'de> {
    type Error = serde_json::Error;

    fn next_key_seed<K: DeserializeSeed<'de>>(&mut self, seed: K) -> Result<Option<K::Value>, Self::Error> {
        match self.entries.next() {
            Some((key, value)) => {
                self.pending = Some(value);
                seed.deserialize(key.as_str().into_deserializer()).map(Some)
            }
            None => Ok(None),
        }
    }

    fn next_value_seed<V: DeserializeSeed<'de>>(&mut self, seed: V) -> Result<V::Value, Self::Error> {
        let value = self.pending.take().ok_or_else(|| de::Error::custom("value requested before key"))?;
        seed.deserialize(GoValue(value))
    }
}

struct ValueSeq<'a> {
    items: std::slice::Iter<'a, Value>,
}

impl<'de> SeqAccess<'de> for ValueSeq<'de> {
    type Error = serde_json::Error;

    fn next_element_seed<T: DeserializeSeed<'de>>(&mut self, seed: T) -> Result<Option<T::Value>, Self::Error> {
        match self.items.next() {
            Some(value) => seed.deserialize(GoValue(value)).map(Some),
            None => Ok(None),
        }
    }

    fn size_hint(&self) -> Option<usize> {
        Some(self.items.len())
    }
}

macro_rules! delegate {
    ($($method:ident),* $(,)?) => {
        $(
            fn $method<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
                self.0.$method(visitor)
            }
        )*
    };
}

impl<'de> Deserializer<'de> for GoValue<'de> {
    type Error = serde_json::Error;

    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Array(items) => visitor.visit_seq(ValueSeq { items: items.iter() }),
            Value::Object(map) => visitor.visit_map(ValueMap { entries: map.iter(), pending: None }),
            other => other.deserialize_any(visitor),
        }
    }

    fn deserialize_struct<V: Visitor<'de>>(self, name: &'static str, fields: &'static [&'static str], visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Object(map) => visitor.visit_map(StructMap { entries: struct_entries(map, fields).into_iter(), pending: None }),
            Value::Null => visitor.visit_map(StructMap { entries: Vec::new().into_iter(), pending: None }),
            other => Err(type_error(other, name)),
        }
    }

    fn deserialize_map<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Object(map) => visitor.visit_map(ValueMap { entries: map.iter(), pending: None }),
            Value::Null => visitor.visit_map(StructMap { entries: Vec::new().into_iter(), pending: None }),
            other => Err(type_error(other, "map")),
        }
    }

    fn deserialize_seq<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Array(items) => visitor.visit_seq(ValueSeq { items: items.iter() }),
            Value::Null => visitor.visit_seq(ValueSeq { items: [].iter() }),
            other => Err(type_error(other, "slice")),
        }
    }

    fn deserialize_option<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_none(),
            _ => visitor.visit_some(self),
        }
    }

    fn deserialize_newtype_struct<V: Visitor<'de>>(self, _name: &'static str, visitor: V) -> Result<V::Value, Self::Error> {
        visitor.visit_newtype_struct(self)
    }

    fn deserialize_string<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_str(""),
            Value::String(s) => visitor.visit_str(s),
            other => Err(type_error(other, "string")),
        }
    }

    fn deserialize_str<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        self.deserialize_string(visitor)
    }

    fn deserialize_bool<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_bool(false),
            Value::Bool(b) => visitor.visit_bool(*b),
            other => Err(type_error(other, "bool")),
        }
    }

    fn deserialize_i64<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_i64(0),
            Value::Number(_) => self.0.deserialize_i64(visitor),
            other => Err(type_error(other, "int")),
        }
    }

    fn deserialize_u64<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_u64(0),
            Value::Number(_) => self.0.deserialize_u64(visitor),
            other => Err(type_error(other, "uint")),
        }
    }

    fn deserialize_f64<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_f64(0.0),
            Value::Number(_) => self.0.deserialize_f64(visitor),
            other => Err(type_error(other, "float64")),
        }
    }

    fn deserialize_i32<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_i32(0) } else { self.0.deserialize_i32(visitor) }
    }
    fn deserialize_u32<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_u32(0) } else { self.0.deserialize_u32(visitor) }
    }
    fn deserialize_u8<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_u8(0) } else { self.0.deserialize_u8(visitor) }
    }
    fn deserialize_u16<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_u16(0) } else { self.0.deserialize_u16(visitor) }
    }
    fn deserialize_i8<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_i8(0) } else { self.0.deserialize_i8(visitor) }
    }
    fn deserialize_i16<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_i16(0) } else { self.0.deserialize_i16(visitor) }
    }
    fn deserialize_f32<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Self::Error> {
        if self.0.is_null() { visitor.visit_f32(0.0) } else { self.0.deserialize_f32(visitor) }
    }

    fn deserialize_enum<V: Visitor<'de>>(self, name: &'static str, variants: &'static [&'static str], visitor: V) -> Result<V::Value, Self::Error> {
        self.0.deserialize_enum(name, variants, visitor)
    }

    fn deserialize_tuple<V: Visitor<'de>>(self, _len: usize, visitor: V) -> Result<V::Value, Self::Error> {
        self.deserialize_seq(visitor)
    }

    fn deserialize_tuple_struct<V: Visitor<'de>>(self, _name: &'static str, _len: usize, visitor: V) -> Result<V::Value, Self::Error> {
        self.deserialize_seq(visitor)
    }

    fn deserialize_unit_struct<V: Visitor<'de>>(self, name: &'static str, visitor: V) -> Result<V::Value, Self::Error> {
        self.0.deserialize_unit_struct(name, visitor)
    }

    delegate!(deserialize_char, deserialize_bytes, deserialize_byte_buf, deserialize_unit, deserialize_identifier, deserialize_ignored_any);
}
