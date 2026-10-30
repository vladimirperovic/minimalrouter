//! The subset of Go 1.22 `http.ServeMux` patterns the router uses:
//! `"METHOD /literal/{name}"`, trailing-slash subtrees, GET also serving
//! HEAD, 405 with a sorted Allow header, and the most specific match winning.

use std::sync::Arc;

use super::{Request, Response};

pub type Handler = Arc<dyn Fn(&mut Request<'_>) -> Response + Send + Sync>;

enum Segment {
    Literal(String),
    Wildcard(String),
}

struct Route {
    method: Option<String>,
    segments: Vec<Segment>,
    subtree: bool,
    handler: Handler,
}

#[derive(Default)]
pub struct Router {
    routes: Vec<Route>,
}

impl Router {
    pub fn new() -> Router {
        Router::default()
    }

    pub fn handle(&mut self, pattern: &str, handler: Handler) {
        let (method, path) = match pattern.split_once(' ') {
            Some((m, p)) => (Some(m.to_string()), p.trim()),
            None => (None, pattern),
        };
        let subtree = path.ends_with('/');
        let segments = path
            .trim_matches('/')
            .split('/')
            .filter(|s| !s.is_empty())
            .map(|s| match s.strip_prefix('{').and_then(|s| s.strip_suffix('}')) {
                Some(name) => Segment::Wildcard(name.to_string()),
                None => Segment::Literal(s.to_string()),
            })
            .collect();
        self.routes.push(Route { method, segments, subtree, handler });
    }

    pub fn handle_fn(&mut self, pattern: &str, handler: impl Fn(&mut Request<'_>) -> Response + Send + Sync + 'static) {
        self.handle(pattern, Arc::new(handler));
    }

    fn matches(route: &Route, segments: &[String], trailing_slash: bool) -> Option<Vec<(String, String)>> {
        if route.subtree {
            if segments.len() < route.segments.len() || (segments.len() == route.segments.len() && !trailing_slash && !route.segments.is_empty()) {
                return None;
            }
        } else if segments.len() != route.segments.len() || (trailing_slash && !segments.is_empty()) {
            return None;
        }
        let mut params = Vec::new();
        for (pattern, value) in route.segments.iter().zip(segments) {
            match pattern {
                Segment::Literal(l) if l == value => {}
                Segment::Literal(_) => return None,
                Segment::Wildcard(name) => params.push((name.clone(), value.clone())),
            }
        }
        Some(params)
    }

    /// Literal segments outrank wildcards; exact patterns outrank subtrees.
    fn specificity(route: &Route) -> (bool, usize, usize) {
        let literals = route.segments.iter().filter(|s| matches!(s, Segment::Literal(_))).count();
        (!route.subtree, route.segments.len(), literals)
    }

    pub fn dispatch(&self, request: &mut Request<'_>) -> Response {
        let trailing_slash = request.raw_path.len() > 1 && request.raw_path.ends_with('/');
        let segments: Vec<String> = request.raw_path.split('/').filter(|s| !s.is_empty()).map(|s| super::unescape(s, false).unwrap_or_default()).collect();
        let cleaned = crate::goish::filepath::clean(&request.raw_path);
        let cleaned = if trailing_slash && cleaned != "/" { format!("{}/", cleaned) } else { cleaned };
        if request.raw_path.starts_with('/') && cleaned != request.raw_path && request.method != "CONNECT" {
            let location = if request.raw_query.is_empty() { cleaned } else { format!("{}?{}", cleaned, request.raw_query) };
            return Response::bytes(301, "text/html; charset=utf-8", format!("<a href=\"{}\">Moved Permanently</a>.\n\n", location).into_bytes())
                .with_header("Location", location);
        }
        let mut path_matches: Vec<(&Route, Vec<(String, String)>)> =
            self.routes.iter().filter_map(|r| Self::matches(r, &segments, trailing_slash).map(|p| (r, p))).collect();
        if path_matches.is_empty() {
            return Response::error("404 page not found", 404);
        }
        path_matches.sort_by_key(|(r, _)| std::cmp::Reverse(Self::specificity(r)));
        let method_ok = |route: &Route| match &route.method {
            None => true,
            Some(m) => m == &request.method || (m == "GET" && request.method == "HEAD"),
        };
        // The most specific path shape decides; among equally specific
        // routes the method selects.
        let best = Self::specificity(path_matches[0].0);
        let candidates: Vec<_> = path_matches.iter().filter(|(r, _)| Self::specificity(r) == best).collect();
        let chosen = candidates.iter().copied().find(|(r, _)| method_ok(r)).or_else(|| path_matches.iter().find(|(r, _)| method_ok(r)));
        if let Some((route, params)) = chosen {
            request.params = params.clone();
            return (route.handler)(request);
        }
        let mut allowed: Vec<String> = path_matches.iter().filter_map(|(r, _)| r.method.clone()).collect();
        if allowed.iter().any(|m| m == "GET") {
            allowed.push("HEAD".into());
        }
        allowed.sort();
        allowed.dedup();
        Response::error("Method Not Allowed", 405).with_header("Allow", allowed.join(", "))
    }
}
