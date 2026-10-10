import sqlite3
import jobwatch_ops as ops


def test_report_preserves_unknowns_and_reports_relations():
    conn = sqlite3.connect(":memory:")
    conn.executescript("""
    CREATE TABLE source_scans(id INTEGER,provider TEXT,company_slug TEXT,status TEXT,scope TEXT);
    INSERT INTO source_scans VALUES(1,'ashby','acme','failed','board_snapshot');
    INSERT INTO source_scans VALUES(2,'ashby','acme','success','board_snapshot');
    CREATE TABLE jobs(raw TEXT,manual_jd_text TEXT,last_seen_at TEXT);
    INSERT INTO jobs VALUES('{}','','');
    INSERT INTO jobs VALUES('{}','Pasted JD','2026-10-10T00:00:00Z');
    CREATE TABLE job_relations(job_id INTEGER,related_job_id INTEGER,evidence TEXT,confirmed INTEGER);
    INSERT INTO job_relations VALUES(2,1,'same_company_title_location',0);
    """)
    report = ops.source_report(conn)
    assert len(report["source_scans"]) == 1
    assert report["source_scans"][0]["status"] == "success"
    assert report["missing_captured_description"] == 1
    assert report["unknown_last_seen"] == 1
    assert report["unconfirmed_relations"] == 1



def timeline_db():
    conn = sqlite3.connect(":memory:")
    conn.executescript("""
    CREATE TABLE jobs(id INTEGER PRIMARY KEY,title TEXT,company_name TEXT,url TEXT,email_to TEXT,status TEXT);
    INSERT INTO jobs VALUES(1,'Backend Engineer','Acme','https://example.test/job','hr@example.test','applied');
    CREATE TABLE application_events(id INTEGER PRIMARY KEY,job_id INTEGER,kind TEXT,detail TEXT,occurred_at TEXT,anchor_id INTEGER,UNIQUE(kind,anchor_id));
    """)
    return conn


def test_followups_wait_and_stop_on_reply_and_terminal_status():
    conn = timeline_db()
    ops.record_event(conn, 1, "contact_sent", "Sent to recruiter", "2026-10-01T00:00:00Z")
    assert ops.due_followups(conn, ops.utc_time("2026-10-07T00:00:00Z")) == []
    assert len(ops.due_followups(conn, ops.utc_time("2026-10-08T00:00:00Z"))) == 1
    conn.execute("UPDATE jobs SET status='rejected'")
    assert ops.due_followups(conn, ops.utc_time("2026-10-09T00:00:00Z")) == []
    conn.execute("UPDATE jobs SET status='applied'")
    ops.record_event(conn, 1, "reply_received", "Recruiter replied", "2026-10-08T00:00:00Z")
    assert ops.due_followups(conn, ops.utc_time("2026-10-09T00:00:00Z")) == []


def test_local_followup_drafts_are_idempotent_and_do_not_count_as_sent(tmp_path):
    conn = timeline_db()
    ops.record_event(conn, 1, "contact_sent", "Sent", "2026-10-01T00:00:00Z")
    now = ops.utc_time("2026-10-08T00:00:00Z")
    drafts = ops.due_followups(conn, now)
    paths = ops.write_followups(conn, drafts, tmp_path, now)
    assert len(paths) == 1
    assert ops.due_followups(conn, now) == []
    assert conn.execute("SELECT count(*) FROM application_events WHERE kind='followup_sent'").fetchone()[0] == 0
    ops.record_event(conn,1,"followup_sent","Confirmed sent manually","2026-10-09T00:00:00Z")
    assert len(ops.due_followups(conn,ops.utc_time("2026-10-16T00:00:00Z"))) == 1
    ops.record_event(conn,1,"followup_sent","Second sent manually","2026-10-17T00:00:00Z")
    assert ops.due_followups(conn,ops.utc_time("2026-10-24T00:00:00Z")) == []


def test_timeline_rejects_unknown_jobs_and_naive_timestamps():
    import pytest
    conn = timeline_db()
    with pytest.raises(ValueError,match="Job not found"):
        ops.record_event(conn,999,"note","Something","2026-10-01T00:00:00Z")
    with pytest.raises(ValueError,match="timezone"):
        ops.record_event(conn,1,"note","Something","2026-10-01T00:00:00")



def test_followup_recipient_comes_from_sent_contact_not_unsent_draft():
    conn = timeline_db()
    ops.record_event(conn,1,"contact_sent","Confirmed send","2026-10-01T00:00:00Z",to="recruiter-a@example.test")
    conn.execute("UPDATE jobs SET email_to='recruiter-b@example.test'")
    draft = ops.due_followups(conn,ops.utc_time("2026-10-09T00:00:00Z"))[0]
    assert draft["to"] == "recruiter-a@example.test"


def test_unknown_recipient_and_naive_legacy_timestamp_are_not_inferred():
    conn = timeline_db()
    ops.record_event(conn,1,"contact_sent","Sent externally","2026-10-01T00:00:00Z")
    assert ops.due_followups(conn,ops.utc_time("2026-10-09T00:00:00Z"))[0]["to"] == ""
    conn.execute("UPDATE application_events SET occurred_at='2026-10-01T00:00:00'")
    assert ops.due_followups(conn,ops.utc_time("2026-10-09T00:00:00Z")) == []
