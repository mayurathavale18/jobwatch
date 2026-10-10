import json
import os
import sqlite3
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import pytest

import production_shadow as shadow


@pytest.fixture
def setup(tmp_path, monkeypatch):
    monkeypatch.setenv("JOBWATCH_JEV_SHADOW_ENABLED", "1")
    monkeypatch.setenv("JEV_API_KEY", "fake-test-key")
    facts = tmp_path / "facts.md"
    facts.write_text("# production\nImplemented Go authentication middleware\n", encoding="utf-8")
    return {"db_path": tmp_path / "shadow.db", "facts_path": facts}


def response(input_tokens=100):
    return {"model": "jev-1.13.0", "answers": {"responsibility_fit": {"type": "choice", "choice": "strong_match",
        "confidence": .8, "probabilities": {"strong_match": .9, "partial_match": .05, "insufficient_evidence": .05}}},
        "usage": {"input_tokens": input_tokens, "output_tokens": 5}}


def fake_requester(body, key, timeout):
    assert timeout == 10 and key == "fake-test-key"
    assert json.loads(body)["state"]["candidate_evidence"]
    return response()


def slow_requester(*args, **kwargs):
    time.sleep(.5)
    return response()


def failing_requester(*args, **kwargs):
    raise TimeoutError("fake-test-key private response")


def excessive_requester(*args, **kwargs):
    return response(90_000)


def hanging_requester(body, key, timeout):
    Path(os.environ["JOBWATCH_TEST_HANG_MARKER"]).write_text("entered", encoding="utf-8")
    while True:
        time.sleep(.1)


def observe(setup, job_id="one", requester=None, **kwargs):
    return shadow.observe({"id": job_id, "title": "Backend Engineer"}, "Build Go authentication middleware",
                          requester=requester or fake_requester, **setup, **kwargs)


def rows(setup):
    with sqlite3.connect(setup["db_path"]) as conn:
        conn.row_factory = sqlite3.Row
        return [dict(row) for row in conn.execute("SELECT * FROM observations")]


def test_disabled_has_no_effects_or_shared_imports(tmp_path, monkeypatch):
    monkeypatch.delenv("JOBWATCH_JEV_SHADOW_ENABLED", raising=False)
    monkeypatch.setattr(shadow, "_observe", lambda *_: pytest.fail("Enabled path reached"))
    assert shadow.observe({}, None, db_path=tmp_path / "never.db") == {"status": "disabled"}
    assert shadow.record_legacy("anything", {}, db_path=tmp_path / "never.db") == {"status": "disabled"}
    assert list(tmp_path.iterdir()) == []


def test_completed_observation_is_advisory_source_backed_and_deduplicated(setup):
    first = observe(setup)
    assert first["status"] == "completed"
    assert observe(setup)["status"] == "deduplicated"
    record = rows(setup)[0]
    body = json.loads(record["payload"])
    fact = body["state"]["candidate_evidence"][0]
    assert fact["context_text"] == "Implemented Go authentication middleware"
    assert fact["headings"] == ["production"] and fact["source_level"] == "production"
    result = json.loads(record["result"])
    assert result["advisory_only"] and not result["confidence_calibrated"]
    assert record["reserved_tokens"] >= len(json.dumps(body, ensure_ascii=False).encode())
    assert "fake-test-key" not in json.dumps(record)


def test_concurrent_same_request_only_bills_once(setup):
    with ThreadPoolExecutor(max_workers=2) as executor:
        first = executor.submit(observe, setup, requester=slow_requester)
        for _ in range(100):
            if setup["db_path"].is_file():
                try:
                    if rows(setup):
                        break
                except sqlite3.OperationalError:
                    pass
            time.sleep(.01)
        second = executor.submit(observe, setup, requester=slow_requester).result(timeout=5)
        assert first.result(timeout=5)["status"] == "completed"
    assert second["status"] == "deduplicated" and len(rows(setup)) == 1


def test_daily_request_and_token_reservations_are_enforced(setup, monkeypatch):
    monkeypatch.setattr(shadow, "MAX_DAILY_REQUESTS", 1)
    assert observe(setup)["status"] == "completed"
    assert observe(setup, job_id="two")["status"] == "budget_exhausted"
    monkeypatch.setattr(shadow, "MAX_DAILY_REQUESTS", 20)
    monkeypatch.setattr(shadow, "MAX_DAILY_TOKENS", rows(setup)[0]["reserved_tokens"] + 1)
    assert observe(setup, job_id="three")["status"] == "budget_exhausted"
    assert sum(record["reserved_tokens"] > 0 for record in rows(setup)) == 1


def test_timeout_failure_is_redacted_fail_open_and_not_retried(setup):
    assert observe(setup, requester=failing_requester)["status"] == "error"
    assert observe(setup, requester=failing_requester)["status"] == "deduplicated"
    record = rows(setup)[0]
    assert record["reserved_tokens"] > 0 and record["error_code"] == "request_outcome_uncertain"
    assert "fake-test-key" not in json.dumps(record)


def test_familiar_planned_unresolved_and_incomplete_contexts_never_sent(setup):
    setup["facts_path"].write_text("# familiar\nGo authentication concepts only\n", encoding="utf-8")
    assert observe(setup, requester=lambda *_args, **_kwargs: pytest.fail("Paid HTTP reached"))["status"] == "no_evidence"
    payload = json.loads(rows(setup)[0]["payload"])
    assert payload["state"]["skipped_contexts"][0]["reasons"] == ["requires_review"]
    setup["facts_path"].write_text("# production\nGo authentication [FILL: verify]\n", encoding="utf-8")
    assert observe(setup)["status"] == "no_evidence"
    setup["facts_path"].write_text("# production\nGo authentication\n" + "more context\n" * 10, encoding="utf-8")
    assert observe(setup)["status"] == "no_evidence"


def test_changed_sources_make_new_hash_and_legacy_is_recorded_without_api(setup):
    first = observe(setup)
    setup["facts_path"].write_text("# production\nImplemented Go authentication middleware and APIs\n", encoding="utf-8")
    second = observe(setup)
    assert first["request_hash"] != second["request_hash"]
    assert shadow.record_legacy(second["request_hash"], {"verdict": "good"}, setup["db_path"])["status"] == "recorded"
    assert rows(setup)[0]["legacy_judgment"] is None
    assert json.loads(rows(setup)[1]["legacy_judgment"]) == {"verdict": "good"}


def test_usage_exceeding_reservation_blocks_more_requests(setup):
    assert observe(setup, requester=excessive_requester)["status"] == "completed_over_reservation"
    assert observe(setup, job_id="two")["status"] == "budget_exhausted"


def test_key_missing_visible_and_job_database_never_touched(setup, tmp_path, monkeypatch):
    monkeypatch.delenv("JEV_API_KEY")
    jobs_db = tmp_path / "jobs.db"
    jobs_db.write_bytes(b"untouched")
    assert observe(setup)["status"] == "key_missing"
    assert rows(setup)[0]["reserved_tokens"] == 0 and jobs_db.read_bytes() == b"untouched"


def test_unpaid_key_and_daily_budget_skips_can_later_become_eligible(setup, monkeypatch):
    monkeypatch.delenv("JEV_API_KEY")
    assert observe(setup)["status"] == "key_missing"
    monkeypatch.setenv("JEV_API_KEY", "fake-test-key")
    assert observe(setup)["status"] == "completed"
    monkeypatch.setattr(shadow, "MAX_DAILY_REQUESTS", 1)
    assert observe(setup, job_id="two")["status"] == "budget_exhausted"
    monkeypatch.setattr(shadow, "utc_day", lambda: "2099-01-01")
    assert observe(setup, job_id="two")["status"] == "completed"
    assert len(rows(setup)) == 2


def test_unavailable_jd_no_clauses_and_storage_failure_are_fail_open(setup, monkeypatch):
    assert observe(setup, jd_unavailable=True)["status"] == "jd_unavailable"
    assert shadow.observe({"id": "two"}, "No useful responsibility text", **setup)["status"] == "no_selected_clauses"
    monkeypatch.setattr(shadow, "connection", lambda _: (_ for _ in ()).throw(OSError("private")))
    assert observe(setup, job_id="three") == {"status": "shadow_error"}


def test_total_deadline_reaps_never_finishing_request_and_legacy_continues(setup, monkeypatch):
    marker = setup["db_path"].with_name("entered.txt")
    monkeypatch.setenv("JOBWATCH_TEST_HANG_MARKER", str(marker))
    monkeypatch.setattr(shadow, "REQUEST_DEADLINE_SECONDS", 1)
    started = time.monotonic()
    result = observe(setup, requester=hanging_requester)
    assert result["status"] == "error" and time.monotonic() - started < 3
    assert marker.read_text(encoding="utf-8") == "entered"
    assert shadow.record_legacy(result["request_hash"], {"verdict": "continued"}, setup["db_path"])["status"] == "recorded"
    assert rows(setup)[0]["reserved_tokens"] > 0
    assert observe(setup, requester=hanging_requester)["status"] == "deduplicated"
    assert not any(child.is_alive() for child in shadow.multiprocessing.active_children())
