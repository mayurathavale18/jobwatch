"""Local Jobwatch diagnostics. No polling, account access or message sending."""
import argparse
import json
import sqlite3
from pathlib import Path
from datetime import datetime, timedelta, timezone
from tailor_resume import jd_text_from_raw, MIN_USABLE_JD_CHARS


def source_report(conn):
    conn.row_factory = sqlite3.Row
    scans = [dict(row) for row in conn.execute("""SELECT * FROM source_scans WHERE id IN
        (SELECT max(id) FROM source_scans GROUP BY provider,company_slug) ORDER BY provider,company_slug""")]
    missing = 0
    thin = 0
    unusable = 0
    unknown_seen = 0
    total = 0
    for row in conn.execute("SELECT raw,manual_jd_text,last_seen_at FROM jobs"):
        total += 1
        manual = (row["manual_jd_text"] or "").strip()
        captured = jd_text_from_raw(row["raw"])
        missing += not bool(manual or captured)
        thin += not bool(manual) and 0 < len(captured) < MIN_USABLE_JD_CHARS
        unusable += not bool(manual) and len(captured) < MIN_USABLE_JD_CHARS
        unknown_seen += not bool(row["last_seen_at"])
    relations = [dict(row) for row in conn.execute("""SELECT job_id,related_job_id,evidence,confirmed
        FROM job_relations ORDER BY job_id DESC LIMIT 50""")]
    count = conn.execute("SELECT count(*) FROM job_relations WHERE confirmed=0").fetchone()[0]
    return {"source_scans": scans, "jobs": total, "missing_captured_description": missing,
            "thin_captured_description": thin, "missing_usable_description": unusable,
            "unknown_last_seen": unknown_seen, "unconfirmed_relations": count, "recent_relations": relations,
            "coverage_note": "Scan success and last_seen do not prove complete coverage or that a job is still open."}



def utc_time(value):
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("Timestamp requires a timezone")
    return parsed.astimezone(timezone.utc)


def record_event(conn, job_id, kind, detail, at, to=""):
    if kind not in {"contact_sent", "reply_received", "followup_sent", "note"}:
        raise ValueError("Unsupported event kind")
    if conn.execute("SELECT 1 FROM jobs WHERE id=?", (job_id,)).fetchone() is None:
        raise ValueError("Job not found")
    if not detail.strip():
        raise ValueError("An event requires supporting detail")
    if kind in {"contact_sent", "followup_sent"}:
        detail = json.dumps({"to": to, "note": detail})
    conn.execute("INSERT INTO application_events(job_id,kind,detail,occurred_at) VALUES(?,?,?,?)",
                 (job_id, kind, detail, utc_time(at).isoformat()))


def due_followups(conn, now):
    conn.row_factory = sqlite3.Row
    drafts = []
    for job in conn.execute("SELECT id,title,company_name,url,email_to FROM jobs WHERE status IN ('new','shortlisted','applied')"):
        events = [dict(e) for e in conn.execute("SELECT * FROM application_events WHERE job_id=? ORDER BY julianday(occurred_at),id", (job["id"],))]
        if any(e["kind"] == "reply_received" for e in events):
            continue
        contacts = [e for e in events if e["kind"] in {"contact_sent", "followup_sent"}]
        if not contacts or sum(e["kind"] == "followup_sent" for e in events) >= 2:
            continue
        # Unknown legacy send timestamps cannot establish a safe follow-up deadline.
        try:
            anchor = max(contacts, key=lambda e: (utc_time(e["occurred_at"]), e["id"]))
            sent_at = utc_time(anchor["occurred_at"])
        except ValueError:
            continue
        if now - sent_at < timedelta(days=7):
            continue
        if any(e["kind"] == "followup_drafted" and e["anchor_id"] == anchor["id"] for e in events):
            continue
        try:
            contact = json.loads(anchor["detail"])
            recipient = contact.get("to", "") if isinstance(contact, dict) else ""
        except (ValueError, TypeError):
            recipient = ""
        drafts.append({"job_id": job["id"], "anchor_id": anchor["id"], "to": recipient,
                       "subject": f"Following up: {job['title']} at {job['company_name']}",
                       "body": f"Hi,\n\nFollowing up on my earlier message about the {job['title']} role at {job['company_name']}. I remain interested and would appreciate an update on next steps.\n\nRole: {job['url']}\n\nThank you,\nMayur Athavale"})
    return drafts


def write_followups(conn, drafts, directory, now):
    directory.mkdir(parents=True, exist_ok=True)
    for draft in drafts:
        path = directory / f"job-{draft['job_id']}-followup-{draft['anchor_id']}.json"
        path.write_text(json.dumps(draft, indent=2), encoding="utf-8")
        conn.execute("INSERT INTO application_events(job_id,kind,detail,occurred_at,anchor_id) VALUES(?,?,?,?,?)",
                     (draft["job_id"], "followup_drafted", str(path.resolve()), now.isoformat(), draft["anchor_id"]))
    return [str(directory / f"job-{d['job_id']}-followup-{d['anchor_id']}.json") for d in drafts]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["report", "timeline", "record", "followups"])
    parser.add_argument("--db", type=Path, default=Path(__file__).resolve().parent.parent / "jobwatch.db")
    parser.add_argument("--job-id", type=int)
    parser.add_argument("--kind", choices=["contact_sent", "reply_received", "followup_sent", "note"])
    parser.add_argument("--detail", default="")
    parser.add_argument("--to", default="", help="verified recipient for a manually recorded sent contact")
    parser.add_argument("--at", default=datetime.now(timezone.utc).isoformat())
    parser.add_argument("--write-dir", type=Path)
    args = parser.parse_args()
    now = utc_time(args.at)
    writable = args.command == "record" or (args.command == "followups" and args.write_dir is not None)
    mode = "rw" if writable else "ro"
    with sqlite3.connect(args.db.resolve().as_uri() + "?mode=" + mode, uri=True) as conn:
        conn.execute("PRAGMA foreign_keys=ON")
        conn.execute("PRAGMA busy_timeout=10000")
        conn.row_factory = sqlite3.Row
        if args.command == "report":
            result = source_report(conn)
        elif args.command == "timeline":
            if args.job_id is None:
                parser.error("timeline requires --job-id")
            result = [dict(e) for e in conn.execute("SELECT * FROM application_events WHERE job_id=? ORDER BY julianday(occurred_at),id", (args.job_id,))]
        elif args.command == "record":
            if args.job_id is None or args.kind is None:
                parser.error("record requires --job-id and --kind")
            record_event(conn, args.job_id, args.kind, args.detail, args.at, args.to)
            result = {"recorded": True}
        else:
            if writable:
                conn.execute("BEGIN IMMEDIATE")
            result = due_followups(conn, now)
            if writable:
                result = {"local_drafts": write_followups(conn, result, args.write_dir, now)}
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
