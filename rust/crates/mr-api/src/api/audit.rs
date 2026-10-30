//! Audit persistence, the bounded audit of request rejections, and the
//! audit log query endpoint.

use std::collections::HashMap;

use super::*;

/// Rejections are audited at a bounded rate per event type and source, and
/// in total; suppression is marked once per window and counted exactly.
const AUDIT_THROTTLE_WINDOW: Duration = Duration::MINUTE;
const AUDIT_THROTTLE_PER_SOURCE: usize = 10;
const AUDIT_THROTTLE_GLOBAL: usize = 120;
const AUDIT_THROTTLE_MAX_SOURCES: usize = 1024;

#[derive(Default)]
pub(crate) struct AuditThrottle {
    window_start: Option<Time>,
    total: usize,
    per_source: HashMap<String, usize>,
    suppressed: usize,
    marked: bool,
}

#[derive(Default, Debug, PartialEq)]
pub(crate) struct Decision {
    record: bool,
    mark_suppression: bool,
    previous_suppressed: usize,
    previous_window_start: Option<Time>,
}

impl AuditThrottle {
    pub(crate) fn admit(&mut self, key: &str, now: Time) -> Decision {
        let mut decision = Decision::default();
        let expired = match self.window_start {
            None => true,
            Some(start) => now.sub(&start) >= AUDIT_THROTTLE_WINDOW || now.before(&start),
        };
        if expired {
            if self.suppressed > 0 {
                decision.previous_suppressed = self.suppressed;
                decision.previous_window_start = self.window_start;
            }
            self.window_start = Some(now);
            self.total = 0;
            self.per_source.clear();
            self.suppressed = 0;
            self.marked = false;
        }
        let count = self.per_source.get(key).copied();
        if self.total >= AUDIT_THROTTLE_GLOBAL
            || count.unwrap_or(0) >= AUDIT_THROTTLE_PER_SOURCE
            || (count.is_none() && self.per_source.len() >= AUDIT_THROTTLE_MAX_SOURCES)
        {
            self.suppressed += 1;
            if !self.marked {
                self.marked = true;
                decision.mark_suppression = true;
            }
            return decision;
        }
        self.per_source.insert(key.to_string(), count.unwrap_or(0) + 1);
        self.total += 1;
        decision.record = true;
        decision
    }
}

impl Server {
    pub(crate) fn append_audit(&self, event: &str, actor: &str, details: HashMap<String, String>) {
        if config::is_high_volume_audit_event(event) {
            self.append_throttled_audit(event, actor, details, Time::now());
            return;
        }
        self.persist_audit(event, actor, details);
    }

    fn persist_audit(&self, event: &str, actor: &str, details: HashMap<String, String>) {
        let Some(store) = self.credential_store() else { return };
        let details: std::collections::BTreeMap<String, String> = details.into_iter().collect();
        if let Err(err) = store.append_audit_event(event, actor, &details) {
            log::info!("[AUDIT] failed to persist {}: {}", event, err);
        }
    }

    fn append_throttled_audit(&self, event: &str, actor: &str, details: HashMap<String, String>, now: Time) {
        let decision = self.audit_limiter.lock().unwrap_or_else(|e| e.into_inner()).admit(&format!("{}\0{}", event, actor), now);
        let window_seconds = (AUDIT_THROTTLE_WINDOW.0 / 1_000_000_000).to_string();
        if decision.previous_suppressed > 0 {
            let start = decision.previous_window_start.map(|t| t.utc().rfc3339()).unwrap_or_default();
            self.persist_audit(
                "audit.suppressed",
                "local",
                super::details(&[("count", &decision.previous_suppressed.to_string()), ("window_start", &start), ("window_seconds", &window_seconds)]),
            );
        }
        if decision.mark_suppression {
            self.persist_audit(
                "audit.throttled",
                "local",
                super::details(&[("first_event", event), ("first_actor", actor), ("window_seconds", &window_seconds)]),
            );
        }
        if decision.record {
            self.persist_audit(event, actor, details);
        }
    }

    pub(crate) fn handle_get_audit_events(&self, r: &mut Request<'_>) -> Response {
        // Read-only sessions may read the metadata-only audit log.
        if self.request_session(r).is_err() {
            return Response::error("Administrator session required", 403);
        }
        let Some(store) = self.credential_store() else { return Response::error("Audit store unavailable", 503) };
        let values = r.query();
        let mut limit = 100;
        let raw_limit = values.get("limit");
        if !raw_limit.is_empty() {
            match config::go_atoi(&raw_limit) {
                Some(parsed) if (1..=500).contains(&parsed) => limit = parsed,
                _ => return Response::error("limit must be between 1 and 500", 400),
            }
        }
        let mut query = config::AuditQuery {
            limit,
            category: values.get("category"),
            search: values.get("q").trim().to_string(),
            actor: values.get("actor"),
            event_type: values.get("event_type"),
            cursor: values.get("cursor"),
            ..Default::default()
        };
        for name in ["since", "until"] {
            let value = values.get(name);
            if value.is_empty() {
                continue;
            }
            let Some(parsed) = Time::parse_rfc3339(&value) else {
                return Response::error(&format!("{} must be an RFC3339 timestamp", name), 400);
            };
            if name == "since" {
                query.since = Some(parsed);
            } else {
                query.until = Some(parsed);
            }
        }
        if let Err(err) = query.validate() {
            return Response::error(&err.to_string(), 400);
        }
        match store.query_audit_events(&query) {
            Ok(page) => Response::json(200, &page),
            Err(_) => Response::error("Could not read audit events", 500),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn throttle_bounds_each_source_and_reports_suppression() {
        let mut throttle = AuditThrottle::default();
        let start = Time::from_unix(1_800_000_000, 0);
        for _ in 0..AUDIT_THROTTLE_PER_SOURCE {
            assert!(throttle.admit("auth.unauthorized\x00192.0.2.1", start).record);
        }
        let first_drop = throttle.admit("auth.unauthorized\x00192.0.2.1", start);
        assert!(!first_drop.record && first_drop.mark_suppression);
        assert!(!throttle.admit("auth.unauthorized\x00192.0.2.1", start).mark_suppression);
        let next = throttle.admit("auth.unauthorized\x00192.0.2.1", start.add(Duration::MINUTE));
        assert!(next.record);
        assert_eq!((next.previous_suppressed, next.previous_window_start), (2, Some(start)));
    }
}
