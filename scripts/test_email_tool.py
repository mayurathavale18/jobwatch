import io
import json
import os

import pytest

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


@pytest.mark.parametrize("detected_email", ["", "morgan@example.org"])
def test_prepare_instahyre_draft_uses_exact_pdf_and_drafts_only(tmp_path, monkeypatch, detected_email):
    import base64
    from email import policy
    from email.parser import BytesParser
    import sqlite3

    db = tmp_path / "jobwatch.db"
    pdf = b"%PDF-1.7\nsubmitted version\x00"
    with sqlite3.connect(db) as conn:
        conn.executescript("""
        CREATE TABLE jobs (id INTEGER PRIMARY KEY, status TEXT, title TEXT, company_name TEXT,
            manual_jd_text TEXT, email_draft_id TEXT, email_to TEXT, email_sent_at TEXT);
        CREATE TABLE instahyre_applications (job_id INTEGER, recipient_name TEXT, recipient_email TEXT,
            work_link TEXT, resume_name TEXT, resume_pdf BLOB, draft_submitting INTEGER DEFAULT 0);
        INSERT INTO jobs VALUES (42, 'applied', 'Backend Engineer', 'Example Labs',
            'Build APIs in Go', '', '', '');
        """)
        conn.execute("INSERT INTO instahyre_applications VALUES (42, 'Morgan', '', ?, 'submitted.pdf', ?, 0)",
                     ("https://example.org/gateway", pdf))

    monkeypatch.setattr(et.tr, "DB_PATH", db)
    monkeypatch.setattr(et.tr, "_gmail_access_token", lambda: "fake-token")
    monkeypatch.setattr(et, "find_work_email", lambda *args, **kwargs: detected_email)

    def generate(**kwargs):
        evidence = json.loads(kwargs["user_content"])
        assert evidence["recipient_name"] == "Morgan"
        assert evidence["job_description"] == "Build APIs in Go"
        return '{"subject":"Backend Engineer at Example Labs","body":"I applied on Instahyre."}'

    monkeypatch.setattr(et.tr, "call_opencode", generate)
    requests = []

    def gmail(request, timeout):
        assert request.full_url == et.tr.GMAIL_DRAFTS_URL
        assert request.get_method() == "POST"
        requests.append(request)
        return io.BytesIO(b'{"id":"draft-42","message":{"id":"message-42"}}')

    monkeypatch.setattr(et, "urlopen", gmail)
    with et.tr.pr.bind({"revision": 1, "profile": {"identity": {
        "name": "Candidate", "links": [{"label": "Website", "url": "https://example.org"}],
    }}}):
        result = et.prepare_instahyre_draft({"job_id": 42})
        assert result["attached_resume"] and result["draft_id"] == "draft-42"
        assert et.prepare_instahyre_draft({"job_id": 42})["ok"]

    assert len(requests) == 1
    raw = json.loads(requests[0].data)["message"]["raw"]
    mime = BytesParser(policy=policy.default).parsebytes(base64.urlsafe_b64decode(raw))
    assert str(mime["To"] or "") == detected_email
    body = mime.get_body().get_content()
    assert "https://example.org/gateway" in body
    assert "https://example.org\n" in body
    attachment, = mime.iter_attachments()
    assert attachment.get_filename() == "submitted.pdf"
    assert attachment.get_payload(decode=True) == pdf
