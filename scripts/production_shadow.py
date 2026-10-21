"""Disabled-by-default, budgeted Jev observations; never changes pipeline decisions."""
import hashlib
import json
import multiprocessing
import os
import re
import sqlite3
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MAX_DAILY_REQUESTS = 20
MAX_DAILY_TOKENS = 100_000
MAX_REQUEST_BYTES = 16_000
FRAMING_ALLOWANCE = 2048
REQUEST_DEADLINE_SECONDS = 10


def digest(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def utc_day():
    return datetime.now(timezone.utc).date().isoformat()


def _request_process(body, key, requester, output_path):
    """Only a disposable child may perform hosted HTTP."""
    from jev_shadow import request, parse_response
    try:
        model, answer, usage = parse_response((requester or request)(body, key, timeout=10))
        response = {"model": model, "answers": {"responsibility_fit": {
            field: answer[field] for field in ("type", "choice", "confidence", "probabilities")}},
            "usage": {field: usage[field] for field in ("input_tokens", "output_tokens") if field in usage}}
        encoded = json.dumps(response).encode("utf-8")
        if len(encoded) <= 4000:
            Path(output_path).write_bytes(encoded)
    except Exception:
        pass


def deadline_request(body, key, requester=None):
    """Enforce elapsed time, including slow responses, and reap the paid worker."""
    with tempfile.TemporaryDirectory(prefix="jobwatch-shadow-") as directory:
        output_path = Path(directory) / "response.json"
        process = multiprocessing.get_context("spawn").Process(
            target=_request_process, args=(body, key, requester, str(output_path)), daemon=True)
        started = time.monotonic()
        try:
            process.start()
            process.join(max(0, REQUEST_DEADLINE_SECONDS - (time.monotonic() - started)))
            if process.is_alive():
                process.kill()
                process.join()
                raise TimeoutError("Shadow request deadline exceeded; outcome uncertain")
            if process.exitcode != 0 or not output_path.is_file():
                raise RuntimeError("Shadow request outcome uncertain")
            return json.loads(output_path.read_bytes())
        finally:
            if process.pid is not None:
                if process.is_alive():
                    process.kill()
                    process.join()
                process.close()


def selected_clauses(jd):
    """Select bounded verb-led source lines, without guessing omitted requirements."""
    clauses = []
    for line in jd.splitlines():
        text = line.strip()
        if len(text) > 2000:
            continue
        if re.match(r"^(?:[-*•]\s*)?(?:build|design|develop|implement|maintain|operate|lead|manage|investigate|ship|create|improve|write|own)\b", text, re.I):
            if text not in clauses:
                clauses.append(text)
            if len(clauses) == 3:
                break
    # shortcut: English verb-led lines only; expand selection from reviewed missed clauses.
    return clauses


def connection(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path, timeout=1)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("""CREATE TABLE IF NOT EXISTS observations (
        request_hash TEXT PRIMARY KEY, job_id TEXT NOT NULL, day TEXT NOT NULL,
        jd_sha256 TEXT NOT NULL, profile_sha256 TEXT NOT NULL, model TEXT NOT NULL,
        rubric_sha256 TEXT NOT NULL, status TEXT NOT NULL, payload TEXT NOT NULL,
        reserved_tokens INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER,
        result TEXT, legacy_judgment TEXT, error_code TEXT)""")
    conn.commit()
    return conn


def record_legacy(request_hash, judgment, db_path=None):
    if os.environ.get("JOBWATCH_JEV_SHADOW_ENABLED") != "1" or not request_hash:
        return {"status": "disabled"}
    try:
        path = Path(db_path) if db_path else ROOT / "logs" / "shadow.db"
        if not path.is_file():
            return {"status": "missing_observation"}
        with sqlite3.connect(path, timeout=1) as conn:
            cursor = conn.execute("UPDATE observations SET legacy_judgment=? WHERE request_hash=?",
                                  (json.dumps(judgment, sort_keys=True), request_hash))
        return {"status": "recorded" if cursor.rowcount else "missing_observation"}
    except Exception:
        return {"status": "shadow_error"}


def observe(job, jd_text, jd_unavailable=False, legacy_judgment=None, db_path=None, facts_path=None, requester=None, profile_text=None):
    if os.environ.get("JOBWATCH_JEV_SHADOW_ENABLED") != "1":
        return {"status": "disabled"}
    try:
        return _observe(job, jd_text, jd_unavailable, legacy_judgment, db_path, facts_path, requester, profile_text)
    except Exception:
        # Source/provider/database failures cannot alter the existing job workflow.
        return {"status": "shadow_error"}


def _observe(job, jd_text, jd_unavailable, legacy_judgment, db_path, facts_path, requester, profile_text):
    from evidence_ledger import build_ledger
    from rank_shadow import FIT_QUESTION
    from jev_shadow import MODEL, parse_response
    job = dict(job)
    job_id = str(job["id"])
    if not isinstance(jd_text, str) or len(jd_text) > 2_000_000:
        raise ValueError("Invalid bounded JD")
    facts_path = Path(facts_path) if facts_path else ROOT / "resume" / "facts.md"
    profile = profile_text if profile_text is not None else facts_path.read_text(encoding="utf-8")
    jd_hash, profile_hash = digest(jd_text), digest(profile)
    clauses = [] if jd_unavailable else selected_clauses(jd_text)
    state = {"posting_evidence": clauses, "candidate_evidence": [], "skipped_contexts": [],
             "selection_scope": "at_most_three_selected_responsibility_lines"}
    if clauses:
        case_state = {"jd": jd_text, "title": job.get("title", "")}
        case = {"id": job_id, "state": case_state, "state_sha256": digest(json.dumps(case_state, sort_keys=True))}
        ledger = build_ledger([case], profile, [{"id": job_id, "clauses": [{"evidence": text} for text in clauses]}], ("shared-profile" if profile_text is not None else str(facts_path)))
        seen = set()
        for clause in ledger["records"][0]["clauses"]:
            for capability in clause["capabilities"]:
                for fact in capability["candidates"]:
                    context_id = (fact["context_start_line"], fact["context_end_line"])
                    if context_id in seen:
                        continue
                    seen.add(context_id)
                    reasons = [flag for flag, value in (("unresolved", fact["context_unresolved"]),
                        ("incomplete", not fact["context_complete"]), ("requires_review", fact["context_requires_review"])) if value]
                    if reasons:
                        state["skipped_contexts"].append({"fact_id": fact["fact_id"], "reasons": reasons})
                        continue
                    state["candidate_evidence"].append({field: fact[field] for field in (
                        "fact_id", "text", "headings", "source_level", "limitations", "source_sha256", "start_line",
                        "context_text", "context_start_line", "context_end_line", "context_lines", "context_limitations")})
    questions = json.loads(json.dumps(FIT_QUESTION))
    questions["responsibility_fit"]["instructions"] += (
        " Candidate evidence contains exact facts with their surrounding context, headings, knowledge levels and limitations."
        " Judge only these selected responsibilities. A technology list is not proof of implementation or scale."
        " Preserve stated limitations. Skipped context IDs are not evidence. Do not decide job eligibility or resume edits.")
    rubric_hash = digest(json.dumps(questions, sort_keys=True))
    payload = {"model": MODEL, "state": state, "questions": questions}
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request_hash = digest(json.dumps({"job_id": job_id, "jd_sha256": jd_hash, "profile_sha256": profile_hash,
                                    "model": MODEL, "rubric_sha256": rubric_hash, "payload": payload}, sort_keys=True))
    key = os.environ.get("JEV_API_KEY")
    initial_status = None
    if jd_unavailable:
        initial_status = "jd_unavailable"
    elif not clauses:
        initial_status = "no_selected_clauses"
    elif not state["candidate_evidence"]:
        initial_status = "no_evidence"
    elif len(body) > MAX_REQUEST_BYTES:
        initial_status = "request_too_large"
    elif not key or "\n" in key or "\r" in key:
        initial_status = "key_missing"
    path = Path(db_path) if db_path else ROOT / "logs" / "shadow.db"
    conn = connection(path)
    try:
        conn.execute("BEGIN IMMEDIATE")
        prior = conn.execute("SELECT status,reserved_tokens FROM observations WHERE request_hash=?", (request_hash,)).fetchone()
        retry_unpaid = prior and prior[1] == 0 and prior[0] in {"key_missing", "budget_exhausted"} and initial_status is None
        if prior and not retry_unpaid:
            conn.rollback()
            return {"status": "deduplicated", "observation_status": prior[0], "request_hash": request_hash}
        day = utc_day()
        reserved = len(body) + FRAMING_ALLOWANCE
        if initial_status is None:
            requests, tokens, exceeded = conn.execute("""SELECT count(*),
                COALESCE(sum(max(reserved_tokens,COALESCE(input_tokens,0))),0),
                COALESCE(max(COALESCE(input_tokens,0)>reserved_tokens),0)
                FROM observations WHERE day=? AND reserved_tokens>0""", (day,)).fetchone()
            if requests >= MAX_DAILY_REQUESTS or tokens + reserved > MAX_DAILY_TOKENS or exceeded:
                initial_status = "budget_exhausted"
        status = initial_status or "reserved"
        conn.execute("""INSERT INTO observations(request_hash,job_id,day,jd_sha256,profile_sha256,model,
            rubric_sha256,status,payload,reserved_tokens,legacy_judgment) VALUES(?,?,?,?,?,?,?,?,?,?,?)
            ON CONFLICT(request_hash) DO UPDATE SET day=excluded.day,status=excluded.status,
            reserved_tokens=excluded.reserved_tokens,
            legacy_judgment=COALESCE(excluded.legacy_judgment,observations.legacy_judgment)""",
            (request_hash, job_id, day, jd_hash, profile_hash, MODEL, rubric_hash, status,
             json.dumps(payload, ensure_ascii=False), reserved if status == "reserved" else 0,
             json.dumps(legacy_judgment, sort_keys=True) if legacy_judgment is not None else None))
        conn.commit()
        if status != "reserved":
            return {"status": status, "request_hash": request_hash}
        started = time.perf_counter()
        try:
            response = deadline_request(body, key, requester)
            model, answer, usage = parse_response(response)
            result = {"label": answer["choice"], "confidence": answer["confidence"], "probabilities": answer["probabilities"],
                "response_model": model, "usage": usage, "latency_ms": round((time.perf_counter() - started) * 1000, 3),
                "advisory_only": True, "scope": "selected_responsibilities_with_source_context",
                "confidence_calibrated": False, "request_bytes": len(body),
                "estimated_input_cost_usd": usage["input_tokens"] * .042 / 1_000_000}
            status = "completed_over_reservation" if usage["input_tokens"] > reserved else "completed"
            conn.execute("UPDATE observations SET status=?,input_tokens=?,result=? WHERE request_hash=?",
                         (status, usage["input_tokens"], json.dumps(result), request_hash))
        except Exception:
            status = "error"
            conn.execute("UPDATE observations SET status=?,error_code='request_outcome_uncertain' WHERE request_hash=?",
                         (status, request_hash))
        conn.commit()
        return {"status": status, "request_hash": request_hash}
    finally:
        conn.close()
