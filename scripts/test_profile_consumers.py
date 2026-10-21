"""Profile-to-consumer contracts, using only fictional candidate data."""
import base64
import copy
from email import message_from_bytes
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys

import pytest

import email_tool as et
import profile_resume as renderer
import profile_runtime as pr
import tailor_resume as tr


@pytest.fixture(scope="session")
def profile_binary(tmp_path_factory):
    if not shutil.which("go"):
        pytest.skip("Go is required for the CLI/Python integration contract")
    binary = tmp_path_factory.mktemp("profile-cli") / ("jobwatch.exe" if os.name == "nt" else "jobwatch")
    subprocess.run(["go", "build", "-o", str(binary), "./cmd/jobwatch"], cwd=pr.ROOT, check=True)
    return binary


def test_python_loads_validated_snapshot_from_selected_database(profile_binary, candidate, tmp_path, monkeypatch):
    db = tmp_path / "selected.db"
    source = tmp_path / "candidate.json"
    source.write_text(json.dumps(candidate), encoding="utf-8")
    subprocess.run([str(profile_binary), "profile", "import", "-db", str(db), "-file", str(source), "-apply"],
                   check=True, capture_output=True)
    monkeypatch.setenv("JOBWATCH_BINARY", str(profile_binary))
    monkeypatch.setenv("JOBWATCH_DB_PATH", str(db))
    # No config.yaml is needed; the caller has already selected its database.
    monkeypatch.setenv("JOBWATCH_WORKDIR", str(tmp_path))
    loaded = pr.load_snapshot()
    assert loaded["revision"] == 1
    assert loaded["profile"]["identity"]["name"] == "Alex Example"
    assert loaded["profile"]["search"]["maxRequiredExperienceYears"] == 3
    with pr.bind(loaded):
        tex, _, _ = tr.generate_resume({"title": "Backend Engineer"}, {"content_text": "Go APIs"})
        assert "Alex Example" in tex and "Built a Go API" in tex
    paths = subprocess.run([sys.executable, "-c", "import sys;sys.path.insert(0,'scripts');import tailor_resume as tr;print(tr.TAILORED_JSON_PATH);print(tr.OUTPUT_ROOT)"],
                           cwd=pr.ROOT, check=True, capture_output=True, text=True)
    assert str(db) + ".state" in paths.stdout
    monkeypatch.setenv("JOBWATCH_DB_PATH", str(tmp_path / "absent.db"))
    with pytest.raises(pr.ProfileError, match="selected database"):
        pr.load_snapshot()
    assert not (tmp_path / "absent.db").exists()


def test_nested_actions_share_one_snapshot_then_reload(candidate, monkeypatch):
    loads = []
    def load():
        loads.append(True)
        return candidate
    monkeypatch.setattr(pr, "load_snapshot", load)
    @pr.action
    def inner():
        return pr.stamp()
    @pr.action
    def outer():
        return inner(), inner()
    with pr.bind(None):
        first, second = outer()
        assert first == second
        with pytest.raises(pr.ProfileError):
            pr.snapshot()
        outer()
    assert len(loads) == 2


def test_missing_or_incomplete_profile_stops_before_fetch_or_llm(candidate, monkeypatch):
    def unexpected(*args, **kwargs):
        pytest.fail("consumer reached external work before checking profile")
    monkeypatch.setattr(tr, "fetch_jd_text_for_job", unexpected)
    monkeypatch.setattr(tr, "call_opencode", unexpected)
    partial = {"revision": 1, "profile": {"schemaVersion": 1, "identity": {"name": "Alex Example"}}}
    with pr.bind(partial), pytest.raises(pr.ProfileError, match="email"):
        tr.process_job({"id": 1}, {})
    partial["profile"]["identity"]["email"] = "alex@example.com"
    with pr.bind(partial), pytest.raises(pr.ProfileError, match="background evidence"):
        tr.process_job({"id": 1}, {})
    with pr.bind(None):
        monkeypatch.setattr(pr, "load_snapshot", lambda: (_ for _ in ()).throw(pr.ProfileError("missing profile")))
        with pytest.raises(pr.ProfileError, match="missing profile"):
            et.generate({"job_id": 1})


def enrich(candidate):
    candidate = copy.deepcopy(candidate)
    bg = candidate["profile"]["background"]
    bg["evidence"] += [
        {"id": "prior-work", "text": "Reduced report latency by 30% with Go & SQL.", "source": "Fictional", "status": "confirmed"},
        {"id": "review-work", "text": "UNCONFIRMED_SECRET_CLAIM", "source": "Fictional", "status": "needs_review"},
    ]
    bg["experience"].append({"id": "prior-role", "company": "Paper & Ink", "title": "Developer_II",
                             "startDate": "2022-01", "endDate": "2023-12", "evidenceIds": ["prior-work", "review-work"]})
    bg["projects"].append({"id": "confirmed-project", "name": "Reports #2", "status": "confirmed", "evidenceIds": ["prior-work"]})
    candidate["profile"]["outreach"].update(signature="Alex\nSoftware Engineer", instructions="Be brief. Ask one clear question.")
    return candidate


def test_resume_uses_profile_identity_roles_dates_and_confirmed_facts(candidate):
    with pr.bind(enrich(candidate)):
        tex, _, _ = tr.generate_resume({"title": "Backend Engineer"}, {"content_text": "Go, Kafka and Kubernetes APIs"})
        assert "Alex Example" in tex and "alex@example.com" in tex
        assert r"\resumeSubheading{Developer\_II}{2022-01 -- 2023-12}{Paper \& Ink}" in tex
        assert r"30\% with Go \& SQL" in tex
        assert "Example University" in tex and "2019-08 -- 2023-05" in tex
        assert r"Reports \#2" in tex
        assert r"\techSkill{Familiarity with}{Kafka}" in tex
        for excluded in ("Example Project", "UNCONFIRMED_SECRET_CLAIM", "Mayur", "Zono", "5k+", "Kubernetes"):
            assert excluded not in tex
        assert "%%JOBWATCH_" not in tex


def test_unconfirmed_skill_evidence_and_conflicting_aliases_never_upgrade_claim(candidate):
    candidate = enrich(candidate)
    bg = candidate["profile"]["background"]
    bg["skills"].append({"id": "unconfirmed", "name": "Kubernetes", "level": "used", "evidenceIds": ["review-work"]})
    bg["skills"].append({"id": "conflicting", "name": "Different tool", "aliases": ["golang"], "level": "familiar"})
    with pr.bind(candidate):
        assert pr.keyword_level("kubernetes") == "fabrication_risk"
        assert pr.keyword_level("golang") == "fabrication_risk"
        assert pr.keyword_level("kafka") == "hedged_familiar"
        assert "UNCONFIRMED_SECRET_CLAIM" not in pr.candidate_text()
        assert "Example Project" not in pr.candidate_text()
        assert "Reduced report latency" in pr.evidence_text()


def test_preferences_and_experience_gate_follow_profile(candidate, monkeypatch):
    calls = []
    monkeypatch.setattr(tr, "call_opencode", lambda *args, **kwargs: calls.append((args, kwargs)))
    tr.llm_judge("Backend role", "Alex's resume", "Engineer", "Example")
    prompt = json.dumps(calls)
    assert "1200000" in prompt and "Fictional example" in prompt
    assert "21 LPA" not in prompt
    assert tr.exceeds_experience_cap("Requires 5 years experience")
    candidate["profile"]["search"].pop("maxRequiredExperienceYears")
    assert not tr.exceeds_experience_cap("Requires 5 years experience")


def test_email_generation_revision_and_outreach_use_same_approved_candidate(candidate, monkeypatch):
    calls = []
    monkeypatch.setattr(et, "get_job", lambda _: {"title": "Engineer", "company_name": "Example"})
    monkeypatch.setattr(tr, "fetch_jd_text_for_job", lambda _: ("Build reporting APIs", False))
    def llm(*args, **kwargs):
        calls.append(json.dumps([args, kwargs]))
        return '{"subject":"Reporting role","body":"Hello from Alex"}'
    monkeypatch.setattr(tr, "call_opencode", llm)
    with pr.bind(enrich(candidate)):
        assert et.generate({"job_id": 1})["ok"]
        assert et.revise({"subject": "Hi", "body": "Draft", "instruction": "Shorter"})["ok"]
        assert tr.draft_outreach_email("Build reporting APIs", "Approved resume", "Pat", "Engineer", "Example")
    for prompt in calls:
        assert "Alex Example" in prompt and "Be brief. Ask one clear question." in prompt
        assert "Reduced report latency" in prompt
        for excluded in ("UNCONFIRMED_SECRET_CLAIM", "Example Project", "Mayur", "Zono"):
            assert excluded not in prompt


@pytest.mark.parametrize("metadata,attached", [("current", True), ("old", False), ("missing", False)])
def test_composer_attaches_only_current_candidate_pdf(metadata, attached, tmp_path, monkeypatch):
    pdf = tmp_path / "alex_resume.pdf"
    pdf.write_bytes(b"fictional PDF")
    stamp = pr.stamp() if metadata != "missing" else {}
    if metadata == "old":
        stamp["profile_revision"] += 1
    tr.save_tailored({"1": {**stamp, "pdf_path": str(pdf)}})
    monkeypatch.setattr(tr, "_gmail_access_token", lambda: "fake-token")
    monkeypatch.setattr(et, "set_email_fields", lambda *args, **kwargs: None)
    requests = []
    class Response:
        def __enter__(self): return self
        def __exit__(self, *args): pass
        def read(self): return b'{"id":"draft-example","message":{"id":"message-example"}}'
    def request(req, **kwargs):
        requests.append(req)
        return Response()
    monkeypatch.setattr(et, "urlopen", request)
    response = et.save_draft({"job_id": 1, "subject": "Role", "body": "Hello"})
    assert response["ok"] and response["attached_resume"] == attached
    assert len(requests) == 1 and requests[0].get_method() == "POST"
    assert not requests[0].full_url.endswith("/send")
    encoded = json.loads(requests[0].data)["message"]["raw"]
    msg = message_from_bytes(base64.urlsafe_b64decode(encoded))
    assert msg["To"] is None
    assert bool([part for part in msg.walk() if part.get_filename()]) == attached


def test_rebuild_discards_old_candidate_cache_and_edit_history(candidate, monkeypatch):
    tr.save_tailored({"1": {"company": "Example", "title": "Backend Engineer", "url": "https://example.com/job",
                           "status": "done", "instructions": ["OLD CANDIDATE NAME"],
                           "base_tex_fields": {"skills_section": "OLD CANDIDATE SKILLS", "bullets": ["old"], "projects": "old"}}})
    monkeypatch.setattr(tr, "fetch_jd_text_for_job", lambda _: ("Build Go reporting APIs", False))
    monkeypatch.setattr(tr, "compile_tex", lambda _: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda _: 1)
    monkeypatch.setattr(tr, "send_telegram", lambda *args, **kwargs: (True, None))
    monkeypatch.setattr(tr, "apply_instructions", lambda *args: pytest.fail("old candidate instructions replayed"))
    assert tr.rebuild_one("1", mode="update")[0]
    entry = tr.load_tailored()["1"]
    assert pr.matches(entry) and entry["base_profile"] == pr.stamp()
    assert entry["instructions"] == []
    assert entry["previous_profile_instructions"] == ["OLD CANDIDATE NAME"]
    tex = Path(entry["tex_path"]).read_text(encoding="utf-8")
    assert "Alex Example" in tex and "OLD CANDIDATE" not in tex
    assert Path(entry["pdf_path"]).name.startswith("alex_example_resume_")
    assert pr.stamp()["profile_sha256"][:12] in entry["pdf_path"]


def test_outreach_requires_current_profile_resume_before_any_external_work(monkeypatch):
    tr.save_tailored({"1": {"status": "done", "pdf_path": "old.pdf"}})
    monkeypatch.setattr(tr, "fetch_jd_text_for_job", lambda _: pytest.fail("fetched JD for stale resume"))
    with pytest.raises(pr.ProfileError, match="Rebuild this resume"):
        tr.outreach_one("1", "pat@example.com")


def test_plain_text_tex_escaping():
    assert renderer.escape(r"A&B_20% {C} #1 $5 \ ~ ^") == r"A\&B\_20\% \{C\} \#1 \$5 \textbackslash{} \textasciitilde{} \textasciicircum{}"


def test_followup_uses_profile_signature(candidate):
    import jobwatch_ops as ops
    from test_jobwatch_ops import timeline_db
    conn = timeline_db()
    ops.record_event(conn, 1, "contact_sent", "Sent", "2026-10-01T00:00:00Z")
    with pr.bind(enrich(candidate)):
        draft = ops.due_followups(conn, ops.utc_time("2026-10-09T00:00:00Z"))[0]
    assert draft["body"].endswith("Alex\nSoftware Engineer")
    assert "Mayur" not in draft["body"]
    conn.close()


def test_shadow_uses_current_evidence_and_source_notes_without_legacy_file(tmp_path, monkeypatch):
    import production_shadow as shadow
    monkeypatch.setenv("JOBWATCH_JEV_SHADOW_ENABLED", "1")
    monkeypatch.delenv("JEV_API_KEY", raising=False)
    db = tmp_path / "shadow.db"
    result = shadow.observe({"id": "example-job", "title": "Backend Engineer"},
                            "Build a Go API for an internal reporting tool.", db_path=db,
                            facts_path=tmp_path / "missing-legacy-facts.md", profile_text=pr.evidence_text())
    assert result["status"] == "key_missing"
    with sqlite3.connect(db) as conn:
        payload = json.loads(conn.execute("SELECT payload FROM observations").fetchone()[0])
    facts = payload["state"]["candidate_evidence"]
    assert facts and any("Built a Go API" in fact["text"] for fact in facts)
    assert all(fact["source_level"] == "unknown" for fact in facts)
    assert any("Candidate-provided example" in fact["context_text"] for fact in facts)


@pytest.mark.skipif(not (shutil.which("tectonic") and shutil.which("pdfinfo")), reason="Tectonic and Poppler required")
def test_fictional_resume_compiles_to_one_page(candidate, tmp_path):
    with pr.bind(enrich(candidate)):
        tex, _, _ = tr.generate_resume({"title": "Backend Engineer"}, {"content_text": "Go, SQL and Kafka reporting APIs"})
    path = tmp_path / "alex_resume.tex"
    path.write_text(tex, encoding="utf-8")
    ok, output = tr.compile_tex(path)
    assert ok, output
    assert tr.get_page_count(path.with_suffix(".pdf")) == 1
