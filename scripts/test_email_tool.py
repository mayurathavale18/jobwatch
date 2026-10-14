import io
import json
import os

import email_tool as et


def test_parse_subject_body_accepts_fenced_json():
    r = et._parse_subject_body('```json\n{"subject": "Hi", "body": "Hello"}\n```')
    assert r == {"ok": True, "subject": "Hi", "body": "Hello"}


def test_parse_subject_body_rejects_missing_fields_and_none():
    assert et._parse_subject_body('{"subject": "Hi"}')["ok"] is False
    assert et._parse_subject_body(None)["ok"] is False
    assert et._parse_subject_body("not json")["ok"] is False


def test_load_dotenv_parses_export_lines_without_overriding(tmp_path, monkeypatch):
    env = tmp_path / ".env"
    env.write_text('export FOO_ET="bar"\n# comment\n\nBAZ_ET=qux\nexport KEEP_ET=new\n')
    monkeypatch.delenv("FOO_ET", raising=False)
    monkeypatch.delenv("BAZ_ET", raising=False)
    monkeypatch.setenv("KEEP_ET", "old")
    et.load_dotenv(env)
    assert os.environ["FOO_ET"] == "bar"
    assert os.environ["BAZ_ET"] == "qux"
    assert os.environ["KEEP_ET"] == "old"


def test_main_rejects_unknown_action(monkeypatch, capsys):
    monkeypatch.setattr("sys.stdin", io.StringIO('{"action": "nuke"}'))
    et.main()
    out = json.loads(capsys.readouterr().out)
    assert out["ok"] is False and "unknown action" in out["error"]


def test_send_requires_draft_id():
    assert et.send({"job_id": 1, "draft_id": ""})["ok"] is False


def test_main_keeps_log_noise_off_stdout(monkeypatch, capsys):
    monkeypatch.setitem(et.ACTIONS, "noisy", lambda req: (et.tr.log("WARN: chatter"), {"ok": True})[1])
    monkeypatch.setattr("sys.stdin", io.StringIO('{"action": "noisy"}'))
    et.main()
    captured = capsys.readouterr()
    assert json.loads(captured.out) == {"ok": True}
    assert "chatter" in captured.err


def test_application_followup_uses_observed_context_and_persists(tmp_path, monkeypatch):
    import sqlite3
    db = tmp_path / "applications.db"
    monkeypatch.setattr(et.tr, "DB_PATH", db)
    with sqlite3.connect(db) as conn:
        conn.execute("CREATE TABLE platform_applications(job_id INTEGER PRIMARY KEY,snapshot TEXT,draft_subject TEXT,draft_body TEXT,draft_link TEXT)")
        conn.execute("INSERT INTO platform_applications VALUES(1,?, '', '', '')", (json.dumps({"platform": "instahyre", "appliedAt": "October 4, 2026", "sourceStatus": "Application Sent"}),))
    monkeypatch.setattr(et, "get_job", lambda _: {"title": "Engineer", "company_name": "Acme"})
    monkeypatch.setattr(et.tr, "fetch_jd_text_for_job", lambda _: ("Python APIs", False))
    monkeypatch.setattr(et.tr, "load_master_tex", lambda: "Verified resume")
    def llm(**kw):
        assert "instahyre" in kw["user_content"] and "October 4, 2026" in kw["user_content"]
        assert "Do not claim a resume is attached" in kw["system_prompt"]
        return '{"subject":"Follow-up","body":"I applied for the Engineer role."}'
    monkeypatch.setattr(et.tr, "call_opencode", llm)
    assert et.generate({"job_id": 1})["ok"]
    with sqlite3.connect(db) as conn:
        assert conn.execute("SELECT draft_subject,draft_body FROM platform_applications").fetchone() == ("Follow-up", "I applied for the Engineer role.")
    et.save_application_content(1, "Edited", "Human revision", "https://mail.google.com/draft")
    with sqlite3.connect(db) as conn:
        assert conn.execute("SELECT draft_body,draft_link FROM platform_applications").fetchone() == ("Human revision", "https://mail.google.com/draft")


def test_application_prepare_never_sends_and_requires_contact_for_gmail(monkeypatch, tmp_path):
    import sqlite3
    import application_draft as ad
    db = tmp_path / "draft.db"
    monkeypatch.setattr(et.tr, "DB_PATH", db)
    with sqlite3.connect(db) as conn:
        conn.execute("CREATE TABLE platform_applications(job_id INTEGER,draft_subject TEXT,draft_body TEXT)")
        conn.execute("INSERT INTO platform_applications VALUES(1,'Saved subject','Saved body')")
    app = {"contacts": [{"name": "Recruiter", "email": "", "evidence": "Named recruiter"}]}
    monkeypatch.setattr(et, "get_application", lambda _: app)
    monkeypatch.setattr(et, "get_job", lambda _: {"email_sent_at": "", "email_draft_id": ""})
    monkeypatch.setattr(et, "send", lambda _: (_ for _ in ()).throw(AssertionError("must never send")))
    saved = []
    monkeypatch.setattr(et, "save_draft", lambda req: (saved.append(req), {"ok": True})[1])
    assert ad.prepare(1)["status"] == "contact_needed"
    assert not saved
    app["contacts"][0]["email"] = "recruiter@example.test"
    assert ad.prepare(1)["status"] == "gmail_draft"
    assert saved[0]["to"] == "recruiter@example.test" and saved[0]["body"] == "Saved body"
    monkeypatch.setattr(et, "get_job", lambda _: {"email_draft_id": "existing"})
    assert ad.prepare(1)["status"] == "already_processed" and len(saved) == 1
