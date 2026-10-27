//! The `flag` package: `-name value`, `--name=value`, boolean flags, `--`
//! termination, Go's error messages and its usage format, so the command-line
//! tools accept and reject exactly what the Go tools do.

use std::collections::BTreeMap;
use std::io::Write;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ErrorHandling {
    /// `flag.ContinueOnError`: report and return the error.
    Continue,
    /// `flag.ExitOnError`: exit 0 on -h/-help, 2 on any other error.
    Exit,
}

#[derive(Debug, PartialEq, Eq)]
pub enum FlagError {
    /// `flag.ErrHelp`.
    Help,
    Invalid(String),
}

impl std::fmt::Display for FlagError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            FlagError::Help => f.write_str("flag: help requested"),
            FlagError::Invalid(message) => f.write_str(message),
        }
    }
}

impl std::error::Error for FlagError {}

#[derive(Clone)]
enum Value {
    Str(String),
    Bool(bool),
}

struct Flag {
    usage: String,
    default: Value,
    value: Value,
}

pub struct FlagSet {
    name: String,
    handling: ErrorHandling,
    flags: BTreeMap<String, Flag>,
    args: Vec<String>,
    output: Box<dyn Write>,
}

/// strconv.ParseBool.
fn parse_bool(value: &str) -> Option<bool> {
    match value {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

impl FlagSet {
    pub fn new(name: &str, handling: ErrorHandling) -> FlagSet {
        FlagSet { name: name.into(), handling, flags: BTreeMap::new(), args: Vec::new(), output: Box::new(std::io::stderr()) }
    }

    pub fn set_output(&mut self, output: Box<dyn Write>) {
        self.output = output;
    }

    /// `fs.SetOutput(io.Discard)`.
    pub fn discard_output(&mut self) {
        self.output = Box::new(std::io::sink());
    }

    pub fn string(&mut self, name: &str, default: &str, usage: &str) {
        self.flags.insert(name.into(), Flag { usage: usage.into(), default: Value::Str(default.into()), value: Value::Str(default.into()) });
    }

    pub fn bool(&mut self, name: &str, default: bool, usage: &str) {
        self.flags.insert(name.into(), Flag { usage: usage.into(), default: Value::Bool(default), value: Value::Bool(default) });
    }

    pub fn get(&self, name: &str) -> String {
        match self.flags.get(name).map(|f| &f.value) {
            Some(Value::Str(v)) => v.clone(),
            Some(Value::Bool(b)) => b.to_string(),
            None => String::new(),
        }
    }

    pub fn get_bool(&self, name: &str) -> bool {
        matches!(self.flags.get(name).map(|f| &f.value), Some(Value::Bool(true)))
    }

    /// Arguments remaining after the flags.
    pub fn args(&self) -> &[String] {
        &self.args
    }

    pub fn narg(&self) -> usize {
        self.args.len()
    }

    /// `PrintDefaults` preceded by the usage header.
    pub fn usage(&mut self) {
        let mut text = if self.name.is_empty() { "Usage:\n".to_string() } else { format!("Usage of {}:\n", self.name) };
        for (name, flag) in &self.flags {
            let mut line = format!("  -{}", name);
            if matches!(flag.default, Value::Str(_)) {
                line.push_str(" string");
            }
            line.push_str(if line.len() <= 4 { "\t" } else { "\n    \t" });
            line.push_str(&flag.usage.replace('\n', "\n    \t"));
            match &flag.default {
                Value::Str(d) if !d.is_empty() => line.push_str(&format!(" (default {:?})", d)),
                Value::Bool(true) => line.push_str(" (default true)"),
                _ => {}
            }
            text.push_str(&line);
            text.push('\n');
        }
        let _ = self.output.write_all(text.as_bytes());
    }

    fn fail(&mut self, message: String) -> FlagError {
        let _ = writeln!(self.output, "{}", message);
        self.usage();
        FlagError::Invalid(message)
    }

    fn parse_one(&mut self, rest: &mut std::collections::VecDeque<String>) -> Result<bool, FlagError> {
        let Some(s) = rest.front().cloned() else { return Ok(false) };
        if s.len() < 2 || !s.starts_with('-') {
            return Ok(false);
        }
        let mut minuses = 1;
        if s.as_bytes()[1] == b'-' {
            minuses = 2;
            if s.len() == 2 {
                rest.pop_front();
                return Ok(false);
            }
        }
        let name = &s[minuses..];
        if name.is_empty() || name.starts_with('-') || name.starts_with('=') {
            return Err(self.fail(format!("bad flag syntax: {}", s)));
        }
        rest.pop_front();
        let (name, value) = match name.split_once('=') {
            Some((n, v)) => (n.to_string(), Some(v.to_string())),
            None => (name.to_string(), None),
        };
        let Some(kind) = self.flags.get(&name).map(|f| f.default.clone()) else {
            if name == "help" || name == "h" {
                self.usage();
                return Err(FlagError::Help);
            }
            return Err(self.fail(format!("flag provided but not defined: -{}", name)));
        };
        let parsed = match kind {
            Value::Bool(_) => match value {
                None => Value::Bool(true),
                Some(v) => match parse_bool(&v) {
                    Some(b) => Value::Bool(b),
                    None => return Err(self.fail(format!("invalid boolean value {:?} for -{}: parse error", v, name))),
                },
            },
            Value::Str(_) => match value.or_else(|| rest.pop_front()) {
                Some(v) => Value::Str(v),
                None => return Err(self.fail(format!("flag needs an argument: -{}", name))),
            },
        };
        if let Some(flag) = self.flags.get_mut(&name) {
            flag.value = parsed;
        }
        Ok(true)
    }

    pub fn parse(&mut self, args: &[String]) -> Result<(), FlagError> {
        let mut rest: std::collections::VecDeque<String> = args.iter().cloned().collect();
        let result = loop {
            match self.parse_one(&mut rest) {
                Ok(true) => continue,
                Ok(false) => break Ok(()),
                Err(err) => break Err(err),
            }
        };
        self.args = rest.into_iter().collect();
        match (&result, self.handling) {
            (Err(FlagError::Help), ErrorHandling::Exit) => std::process::exit(0),
            (Err(_), ErrorHandling::Exit) => std::process::exit(2),
            _ => result,
        }
    }
}

/// os.Geteuid: -1 where the platform has no user IDs.
pub fn geteuid() -> i64 {
    #[cfg(unix)]
    {
        unsafe { libc::geteuid() as i64 }
    }
    #[cfg(not(unix))]
    {
        -1
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args(values: &[&str]) -> Vec<String> {
        values.iter().map(|v| v.to_string()).collect()
    }

    #[test]
    fn parses_like_go() {
        let mut fs = FlagSet::new("stage", ErrorHandling::Continue);
        fs.discard_output();
        fs.string("dir", "", "directory");
        fs.bool("offline", false, "offline");
        fs.parse(&args(&["--dir", "/x", "-offline", "rest", "-dir=ignored"])).unwrap();
        assert_eq!((fs.get("dir"), fs.get_bool("offline"), fs.args().to_vec()), ("/x".to_string(), true, args(&["rest", "-dir=ignored"])));
        let mut fs = FlagSet::new("stage", ErrorHandling::Continue);
        fs.discard_output();
        fs.string("dir", "", "directory");
        assert_eq!(fs.parse(&args(&["-nope"])).unwrap_err().to_string(), "flag provided but not defined: -nope");
        assert_eq!(fs.parse(&args(&["-dir"])).unwrap_err().to_string(), "flag needs an argument: -dir");
        assert_eq!(fs.parse(&args(&["-h"])).unwrap_err(), FlagError::Help);
        fs.parse(&args(&["--", "-dir", "x"])).unwrap();
        assert_eq!(fs.narg(), 2);
    }
}
