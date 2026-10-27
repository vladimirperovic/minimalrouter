use rusqlite::types::Value as SqlValue;
use rusqlite::{params, params_from_iter};

use super::*;
use crate::config::ErrNoRows;

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Alert {
    pub id: i64,
    pub domain: String,
    pub category: String,
    pub severity: String,
    pub first_seen: i64,
    pub last_seen: i64,
    pub lookups: u64,
    pub last_address: String,
    pub acknowledged_at: i64,
    pub ignored: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AlertPage {
    pub alerts: Vec<Alert>,
    pub total: i64,
    pub offset: i64,
    pub limit: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Exception {
    pub id: i64,
    pub domain: String,
    pub category: String,
}

fn no_rows(err: rusqlite::Error) -> Error {
    match err {
        rusqlite::Error::QueryReturnedNoRows => ErrNoRows.into(),
        other => other.into(),
    }
}

impl Service {
    pub fn alerts(&self, view: &str, category: &str, offset: i64, limit: i64) -> Result<AlertPage> {
        let mut out = AlertPage { offset, limit, ..Default::default() };
        if !self.enabled_settings()?.0 {
            return Ok(out);
        }
        let mut filter = "1=1".to_string();
        let mut args: Vec<SqlValue> = Vec::new();
        if view == "new" {
            filter += " AND acknowledged_at=0 AND NOT EXISTS(SELECT 1 FROM exceptions e WHERE e.domain=a.domain AND e.category=a.category)";
        }
        if !category.is_empty() {
            filter += " AND category=?";
            args.push(category.to_string().into());
        }
        let db = self.lock_db();
        out.total = db.query_row(&format!("SELECT count(*) FROM alerts a WHERE {}", filter), params_from_iter(args.iter()), |r| r.get(0))?;
        args.push(limit.into());
        args.push(offset.into());
        let mut stmt = db.prepare(&format!(
            "SELECT id,domain,category,severity,first_seen,last_seen,lookups,last_address,acknowledged_at,EXISTS(SELECT 1 FROM exceptions e WHERE e.domain=a.domain AND e.category=a.category) FROM alerts a WHERE {} ORDER BY last_seen DESC,id DESC LIMIT ? OFFSET ?",
            filter
        ))?;
        out.alerts = stmt
            .query_map(params_from_iter(args.iter()), |r| {
                Ok(Alert {
                    id: r.get(0)?,
                    domain: r.get(1)?,
                    category: r.get(2)?,
                    severity: r.get(3)?,
                    first_seen: r.get(4)?,
                    last_seen: r.get(5)?,
                    lookups: r.get(6)?,
                    last_address: r.get(7)?,
                    acknowledged_at: r.get(8)?,
                    ignored: r.get(9)?,
                })
            })?
            .collect::<rusqlite::Result<_>>()?;
        Ok(out)
    }

    pub fn acknowledge(&self, id: i64) -> Result<()> {
        let changed = self.lock_db().execute("UPDATE alerts SET acknowledged_at=? WHERE id=?", params![Time::now().unix(), id])?;
        if changed == 0 {
            return Err(ErrNoRows.into());
        }
        Ok(())
    }

    pub fn ignore(&self, id: i64) -> Result<()> {
        let mut db = self.lock_db();
        let tx = db.transaction()?;
        let (domain, category): (String, String) =
            tx.query_row("SELECT domain,category FROM alerts WHERE id=?", params![id], |r| Ok((r.get(0)?, r.get(1)?))).map_err(no_rows)?;
        let others: i64 = tx.query_row("SELECT count(*) FROM exceptions WHERE domain<>? OR category<>?", params![domain, category], |r| r.get(0))?;
        if others >= 500 {
            return Err(errorf!("exception limit reached"));
        }
        tx.execute("INSERT OR IGNORE INTO exceptions(domain,category) VALUES(?,?)", params![domain, category])?;
        tx.commit()?;
        Ok(())
    }

    pub fn exceptions(&self) -> Result<Vec<Exception>> {
        let db = self.lock_db();
        let mut stmt = db.prepare("SELECT id,domain,category FROM exceptions ORDER BY domain,category")?;
        let rows = stmt.query_map([], |r| Ok(Exception { id: r.get(0)?, domain: r.get(1)?, category: r.get(2)? }))?;
        Ok(rows.collect::<rusqlite::Result<_>>()?)
    }

    pub fn remove_exception(&self, id: i64) -> Result<()> {
        if self.lock_db().execute("DELETE FROM exceptions WHERE id=?", params![id])? == 0 {
            return Err(ErrNoRows.into());
        }
        Ok(())
    }
}
