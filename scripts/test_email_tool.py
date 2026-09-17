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
