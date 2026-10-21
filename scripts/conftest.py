"""Keep candidate fixtures, network calls and writable state isolated in tests."""
import json
from pathlib import Path
import sqlite3
import sys

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import profile_runtime as pr
import tailor_resume as tr
import email_tool as et


@pytest.fixture
def candidate():
    return json.loads((Path(__file__).parent / "testdata" / "profile.json").read_text(encoding="utf-8"))


@pytest.fixture(autouse=True)
def isolated_candidate(candidate, tmp_path_factory, monkeypatch):
    tmp_path = tmp_path_factory.mktemp("candidate-runtime")
    db = tmp_path / "jobs.db"
    with sqlite3.connect(db) as conn:
        conn.execute("CREATE TABLE jobs(id INTEGER PRIMARY KEY,status TEXT,fit_score INTEGER,work_mode TEXT)")
        conn.executemany("INSERT INTO jobs(id,status) VALUES(?, 'new')", [(1,), (2,), (3,)])
    monkeypatch.setattr(tr, "DB_PATH", db)
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "resumes")
    def no_network(*args, **kwargs):
        raise OSError("Network disabled in tests")
    monkeypatch.setattr(tr, "urlopen", no_network)
    monkeypatch.setattr(et, "urlopen", no_network)
    with pr.bind(candidate):
        yield
