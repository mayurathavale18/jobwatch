"""Tests for scripts/tailor_resume.py. Run with: pytest scripts/test_tailor_resume.py -v"""
import json
import sqlite3
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tailor_resume as tr


def test_classify_keyword_direct_for_truthful_skill():
    assert tr.classify_keyword("terraform") == "direct"


def test_classify_keyword_hedged_familiar_for_kafka():
    assert tr.classify_keyword("kafka") == "hedged_familiar"


def test_classify_keyword_hedged_adjacent_for_cassandra():
    # Cassandra is one of Mayur's curated "Adjacent Skills" in facts.md
    # (Databases : cassandra) -- not in facts.md's production/used/familiar
    # tiers at all, but a fair hedged claim per his own judgment call.
    assert tr.classify_keyword("cassandra") == "hedged_adjacent"


def test_classify_keyword_fabrication_risk_for_unrelated_tool():
    assert tr.classify_keyword("salesforce") == "fabrication_risk"


def test_score_coverage_tiered_buckets_keywords_correctly():
    keywords = ["terraform", "kafka", "cassandra", "salesforce"]
    resume_text = "Owned AWS infrastructure via Terraform for 5 services."
    result = tr.score_coverage_tiered(keywords, resume_text)

    assert "terraform" in result["direct"]
    assert "kafka" not in result["direct"] and "kafka" not in result["hedged"]
    assert "cassandra" not in result["hedged"]  # not present in resume_text yet
    assert "salesforce" in result["missing"]
    assert result["score"] == 0.25  # only "terraform" actually appears in the text


def test_score_coverage_tiered_counts_hedged_keyword_present_in_text():
    keywords = ["kafka"]
    resume_text = "Has working exposure to Kafka for adjacent messaging needs."
    result = tr.score_coverage_tiered(keywords, resume_text)
    assert "kafka" in result["hedged"]
    assert result["score"] == 1.0


def test_html_to_jd_text_strips_tags_and_scripts():
    html_input = """
    <html><head><script>var x = 1;</script><style>.a{color:red}</style></head>
    <body><nav>Home | About</nav>
    <main><h1>Backend Engineer</h1><p>We need someone who knows Go and Kubernetes.</p></main>
    <footer>© 2026 Example Corp</footer></body></html>
    """
    text = tr.html_to_jd_text(html_input)
    assert "Backend Engineer" in text
    assert "Go and Kubernetes" in text
    assert "var x = 1" not in text
    assert "color:red" not in text


def test_fetch_jd_generic_returns_none_on_fetch_error(monkeypatch):
    def raise_error(*args, **kwargs):
        raise OSError("connection refused")
    monkeypatch.setattr(tr, "urlopen", raise_error)
    assert tr.fetch_jd_generic("https://example.com/job/1") is None


def test_fetch_jd_generic_parses_real_looking_page(monkeypatch):
    fake_html = (
        "<html><body><main><h1>Software Engineer</h1>"
        "<p>Looking for someone with Python and Terraform experience, "
        "5+ years, distributed systems background required.</p></main></body></html>"
    ).encode("utf-8")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return fake_html

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())
    result = tr.fetch_jd_generic("https://valorem.keka.com/careers/jobdetails/124256")
    assert result is not None
    assert "Terraform" in result["content_text"]
    assert result["absolute_url"] == "https://valorem.keka.com/careers/jobdetails/124256"


def test_html_to_jd_text_converts_br_tags_to_newlines():
    html_input = "<p>First line.<br>Second line.<br/>Third line.<br />Fourth line.</p>"
    text = tr.html_to_jd_text(html_input)
    lines = [l.strip() for l in text.split("\n") if l.strip()]
    assert lines == ["First line.", "Second line.", "Third line.", "Fourth line."]


def test_call_opencode_returns_none_without_api_key(monkeypatch):
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "")
    assert tr.call_opencode("system", "user") is None


def test_call_opencode_returns_content_on_success(monkeypatch):
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "fake-key")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            import json as _json
            return _json.dumps({
                "choices": [{"message": {"content": "hello from the model"}}]
            }).encode("utf-8")

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=20: FakeResponse())
    result = tr.call_opencode("system prompt", "user prompt")
    assert result == "hello from the model"


def test_call_opencode_returns_none_on_http_error(monkeypatch):
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "fake-key")
    def raise_error(*args, **kwargs):
        raise OSError("timed out")
    monkeypatch.setattr(tr, "urlopen", raise_error)
    assert tr.call_opencode("system", "user") is None


def test_fetch_jd_generic_falls_back_to_llm_when_scrape_thin(monkeypatch):
    # Simulate a JS-rendered SPA shell: almost no text in the raw HTML.
    thin_html = b"<html><body><div id='root'></div><script src='app.js'></script></body></html>"

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return thin_html

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "fake-key")
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "Backend Engineer role requiring Go, Terraform, and distributed systems experience. We need someone with strong backend skills. This role involves working with cloud infrastructure and managing complex systems. PostgreSQL and Redis are key technologies for this position. You should have proven experience with microservices.",
    )

    result = tr.fetch_jd_generic("https://valorem.keka.com/careers/jobdetails/124256")
    assert result is not None
    assert "Terraform" in result["content_text"]


def test_fetch_jd_generic_skips_llm_when_scrape_is_already_usable(monkeypatch):
    good_html = (
        b"<html><body><main><h1>Backend Engineer</h1>"
        b"<p>" + b"Requires Go and PostgreSQL experience. " * 10 + b"</p></main></body></html>"
    )

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return good_html

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())

    def fail_if_called(*args, **kwargs):
        raise AssertionError("call_opencode should not be called when the scrape is already usable")
    monkeypatch.setattr(tr, "call_opencode", fail_if_called)

    result = tr.fetch_jd_generic("https://example.com/job/1")
    assert "PostgreSQL" in result["content_text"]


def test_llm_judge_parses_valid_json_response(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": ["kubernetes"], "reason": "Strong backend match."}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["verdict"] == "screen"
    assert result["missing_keywords"] == ["kubernetes"]
    assert result["reason"] == "Strong backend match."
    assert result["source"] == "llm"


def test_llm_judge_falls_back_to_rule_based_on_llm_failure(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20: None,
    )
    result = tr.llm_judge(
        "Requires Go, Terraform, Kubernetes, GraphQL, Rust.",
        "Owned AWS infrastructure via Terraform for 5 services.",
        "Backend Engineer", "Acme",
    )
    assert result["source"] == "rule_based"
    assert result["verdict"] in ("screen", "reject_risk")
    assert isinstance(result["missing_keywords"], list)


def test_llm_judge_falls_back_to_rule_based_on_malformed_json(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "not valid json at all",
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["source"] == "rule_based"


def test_apply_instructions_returns_edited_triple_on_success(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, **kwargs: json.dumps({
            "skills_section": "\\techSkill{Languages}{Go, Python}",
            "bullets": ["Built a thing.", "Shipped another thing."],
            "projects": "\\resumeItem{A project.}",
        }),
    )
    (skills, bullets, projects), ok = tr.apply_instructions(
        "\\techSkill{Languages}{Go}", ["Built a thing."], "\\resumeItem{A project.}",
        ["mention Python too"],
    )
    assert ok is True
    assert skills == "\\techSkill{Languages}{Go, Python}"
    assert bullets == ["Built a thing.", "Shipped another thing."]
    assert projects == "\\resumeItem{A project.}"


def test_apply_instructions_falls_back_on_llm_unavailable(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda system_prompt, user_content, **kwargs: None)
    (skills, bullets, projects), ok = tr.apply_instructions(
        "orig skills", ["orig bullet"], "orig projects", ["some instruction"],
    )
    assert ok is False
    assert (skills, bullets, projects) == ("orig skills", ["orig bullet"], "orig projects")


def test_apply_instructions_falls_back_on_malformed_json(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda system_prompt, user_content, **kwargs: "not json")
    (skills, bullets, projects), ok = tr.apply_instructions(
        "orig skills", ["orig bullet"], "orig projects", ["some instruction"],
    )
    assert ok is False
    assert (skills, bullets, projects) == ("orig skills", ["orig bullet"], "orig projects")


def test_apply_instructions_falls_back_on_missing_keys(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, **kwargs: json.dumps({"skills_section": "x"}),
    )
    (skills, bullets, projects), ok = tr.apply_instructions(
        "orig skills", ["orig bullet"], "orig projects", ["some instruction"],
    )
    assert ok is False
    assert (skills, bullets, projects) == ("orig skills", ["orig bullet"], "orig projects")


def test_build_skills_section_lists_jd_matched_keyword_first():
    # Languages is authored as "Go, Python, TypeScript, JavaScript, SQL, Bash" --
    # Python is not first as-authored, so a JD keyword of "python" reordering
    # it to the front proves the JD-match reordering actually happened.
    section_with_match, _ = tr.build_skills_section(["backend"], ["python"])
    section_without_match, _ = tr.build_skills_section(["backend"], [])

    def first_language_in_line(section):
        for line in section.split("\n"):
            if line.startswith("\\techSkill{Languages}"):
                items = line.split("{")[-1].rstrip("}").split(", ")
                return items[0]
        return None

    assert first_language_in_line(section_without_match) == "Go"
    assert first_language_in_line(section_with_match) == "Python"


def test_inject_keyword_emphasis_hedges_familiar_tier_keyword():
    bullets = ["Built a backend service using Go and PostgreSQL."]
    skills_section = "\\techSkill{Languages}{Go, Python}"
    jd_keywords = ["kafka"]  # hedged_familiar tier, not in TRUTHFUL_SKILLS

    new_bullets, extra_line = tr.inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    combined = new_bullets[0] + (extra_line or "")
    assert "Kafka" in combined
    # Must use hedging language, not a plain/confident claim.
    assert any(hedge in combined for hedge in ("exposure to", "familiarity with", "working knowledge of"))


def test_inject_keyword_emphasis_never_touches_fabrication_risk_keyword():
    bullets = ["Built a backend service using Go and PostgreSQL."]
    skills_section = "\\techSkill{Languages}{Go, Python}"
    jd_keywords = ["salesforce"]  # fabrication_risk tier

    new_bullets, extra_line = tr.inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    combined = new_bullets[0] + (extra_line or "")
    assert "Salesforce" not in combined and "salesforce" not in combined


def test_inject_keyword_emphasis_overflow_hedged_keywords_stay_hedged():
    # More than MAX_INJECTED_KEYWORDS (3) hedged-tier gaps -- the overflow
    # must NOT land in the plain "Additional Relevant Skills" line unhedged.
    bullets = ["Built a backend service using Go and PostgreSQL."]
    skills_section = "\\techSkill{Languages}{Go, Python}"
    # kafka, rabbitmq, gcp, firebase are all hedged_familiar; cassandra, bigquery are hedged_adjacent
    jd_keywords = ["kafka", "rabbitmq", "gcp", "firebase", "cassandra", "bigquery"]

    new_bullets, extra_line = tr.inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    combined = new_bullets[0] + (extra_line or "")
    assert "Additional Relevant Skills" not in combined or "Additional Exposure" in combined
    # Nothing from the hedged set should appear as a bare claim outside hedge context
    assert "\\techSkill{Additional Exposure}" in (extra_line or "")


def test_classify_keyword_kubernetes_langchain_mongodb_trpc_not_familiar():
    # These are already plainly claimed in the base skills grid, so they
    # must NOT be in the hedged_familiar tier (that would contradict the
    # resume's own direct claim of them).
    for kw in ("kubernetes", "k8s", "langchain", "mongodb", "trpc"):
        assert tr.classify_keyword(kw) != "hedged_familiar", f"{kw} should not be hedged_familiar"


def test_reword_is_safe_rejects_changed_numbers():
    original = "Handled 1k RPS with 250ms P99 latency."
    reworded = "Handled 5k RPS with 250ms P99 latency."  # number changed: 1k -> 5k
    assert tr.reword_is_safe(original, reworded, approved_keywords=[]) is False


def test_reword_is_safe_rejects_unapproved_keyword():
    original = "Built a backend service using Go."
    reworded = "Built a backend service using Go and Kubernetes."  # kubernetes not approved
    assert tr.reword_is_safe(original, reworded, approved_keywords=["go"]) is False


def test_reword_is_safe_accepts_valid_rewording():
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    reworded = "Owned a production Go API gateway, routing traffic across 12 microservices."
    assert tr.reword_is_safe(original, reworded, approved_keywords=["go", "microservices"]) is True


def test_llm_reword_bullet_uses_llm_output_when_safe(monkeypatch):
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "Owned a Go API gateway routing traffic across 12 microservices, with RESTful design throughout.",
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go", "rest"], hedged_keywords=[])
    assert "RESTful" in result or "REST" in result


def test_llm_reword_bullet_falls_back_to_original_on_unsafe_output(monkeypatch):
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "Built and owned a Kubernetes-based API gateway routing traffic across 50 microservices.",
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go"], hedged_keywords=[])
    assert result == original  # unsafe (unapproved keyword + changed number) -> unchanged


def test_llm_reword_bullet_falls_back_to_original_on_llm_failure(monkeypatch):
    original = "Built and owned a Go-based API gateway."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20: None,
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go"], hedged_keywords=[])
    assert result == original


def test_reword_is_safe_rejects_unrelated_hallucinated_content():
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    reworded = "Led cross-functional stakeholder alignment initiatives for quarterly planning cycles."
    assert tr.reword_is_safe(original, reworded, approved_keywords=[]) is False


def test_llm_reword_bullet_falls_back_to_original_on_empty_after_strip(monkeypatch):
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20: '   ',
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go"], hedged_keywords=[])
    assert result == original


def test_significant_words_strips_latex_markup():
    words = tr._significant_words("Built a \\textbf{Go-based API gateway} routing traffic.")
    assert "textbf" not in words
    assert "based" in words or "gateway" in words


def test_process_job_skips_non_engineering_role(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    job = {"id": 1, "company_name": "Acme", "title": "Account Executive", "url": "https://example.com/1"}
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "skipped_non_eng"
    assert tailored["1"]["status"] == "ignored"


def test_process_job_marks_failed_when_compile_fails(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "output")
    monkeypatch.setattr(
        tr, "fetch_greenhouse_job", lambda slug, gh_id: None,
    )
    monkeypatch.setattr(
        tr, "fetch_jd_generic",
        lambda url: {"title": "", "company_name": "", "content_text": "Backend Engineer role.",
                      "content_html": "", "location": "", "absolute_url": url},
    )
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (False, "fake LaTeX error"))

    job = {"id": 2, "company_name": "Acme", "title": "Backend Engineer", "url": "https://example.com/2"}
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "failed"
    assert tailored["2"]["status"] == "failed"


def test_send_telegram_caption_includes_verdict_and_missing(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TG_TOKEN", "fake-token")
    monkeypatch.setattr(tr, "TG_CHAT", "fake-chat")

    captured = {}
    def fake_run(cmd, capture_output, text, timeout):
        captured["cmd"] = cmd
        class FakeResult:
            stdout = '{"ok": true}'
        return FakeResult()
    monkeypatch.setattr(tr.subprocess, "run", fake_run)

    pdf_path = tmp_path / "resume.pdf"
    pdf_path.write_bytes(b"%PDF-fake")

    judgment = {"verdict": "screen", "reason": "Strong match.", "source": "llm", "missing_keywords": ["kubernetes", "graphql"]}
    ok, err = tr.send_telegram(
        pdf_path, "Acme", "Backend Engineer", "https://example.com/job", 0.8, "42",
        judgment=judgment, hedged_keywords=["kafka"], jd_unavailable=False,
    )
    assert ok is True
    caption = next(v for v in captured["cmd"] if v.startswith("caption="))
    assert "SCREEN" in caption
    assert "Strong match." in caption
    assert "kubernetes" in caption and "graphql" in caption
    assert "Kafka" in caption or "kafka" in caption
    assert "#J42" in caption


def test_send_telegram_caption_flags_jd_unavailable(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TG_TOKEN", "fake-token")
    monkeypatch.setattr(tr, "TG_CHAT", "fake-chat")

    def fake_run(cmd, capture_output, text, timeout):
        class FakeResult:
            stdout = '{"ok": true}'
        return FakeResult()
    monkeypatch.setattr(tr.subprocess, "run", fake_run)

    pdf_path = tmp_path / "resume.pdf"
    pdf_path.write_bytes(b"%PDF-fake")

    ok, _ = tr.send_telegram(
        pdf_path, "Acme", "Backend Engineer", "https://example.com/job", 0.3, "43",
        judgment=None, hedged_keywords=None, jd_unavailable=True,
    )
    assert ok is True


def test_build_and_splice_equals_generate_resume():
    job = {"id": 1, "title": "Backend Engineer", "company_name": "Stripe", "url": "https://stripe.com/jobs/1"}
    jd_data = {
        "title": "Backend Engineer", "company_name": "Stripe",
        "content_text": "We need Go, Kafka, PostgreSQL, and AWS experience.",
        "content_html": "", "location": "", "absolute_url": "https://stripe.com/jobs/1",
    }

    want_master, want_focus, want_keywords = tr.generate_resume(job, jd_data, tight=False)

    skills_section, bullets, projects, focus, jd_keywords = tr.build_resume_fields(job, jd_data, tight=False)
    got_master = tr.splice_resume_fields(tr.load_master_tex(), skills_section, bullets, projects)

    assert got_master == want_master
    assert focus == want_focus
    assert jd_keywords == want_keywords


def _make_empty_jobs_db(path):
    """A real sqlite file with an empty jobs table -- rebuild_one() opens
    DB_PATH with mode=ro, which raises OperationalError on a genuinely
    missing file rather than falling back, so tests need a real file here."""
    conn = sqlite3.connect(path)
    conn.execute("CREATE TABLE jobs (id INTEGER PRIMARY KEY)")
    conn.commit()
    conn.close()


def test_rebuild_one_update_mode_reuses_cached_base_fields(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)
    db_path = tmp_path / "test.db"
    _make_empty_jobs_db(db_path)
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    cached_fields = {
        "skills_section": "\\techSkill{Languages}{Go}",
        "bullets": ["Cached bullet."],
        "projects": "\\resumeItem{Cached project.}",
    }
    tr.save_tailored({
        "1": {
            "company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1",
            "status": "done", "base_tex_fields": cached_fields, "instructions": [],
        }
    })

    build_calls = []
    monkeypatch.setattr(
        tr, "build_resume_fields",
        lambda job, jd_data, tight=False: build_calls.append(1) or (
            "fresh skills", ["fresh bullet"], "fresh projects", "focus", ["kw"]
        ),
    )
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: f"MASTER::{s}::{b}::{p}")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="update", instruction=None)

    assert ok is True
    assert err is None
    assert build_calls == []  # cached base fields were reused, not rebuilt

    tailored = tr.load_tailored()
    assert "MASTER::\\techSkill{Languages}{Go}::['Cached bullet.']::\\resumeItem{Cached project.}" == open(
        tailored["1"]["tex_path"], encoding="utf-8"
    ).read()


def test_rebuild_one_persists_and_applies_instruction(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)
    db_path = tmp_path / "test.db"
    _make_empty_jobs_db(db_path)
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    monkeypatch.setattr(
        tr, "build_resume_fields",
        lambda job, jd_data, tight=False: ("base skills", ["base bullet"], "base projects", "focus", ["kw"]),
    )
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    applied_with = {}

    def fake_apply_instructions(skills, bullets, projects, instructions):
        applied_with["instructions"] = list(instructions)
        return (skills, bullets, projects), True

    monkeypatch.setattr(tr, "apply_instructions", fake_apply_instructions)

    sent_kwargs = {}

    def fake_send_telegram(*a, **kw):
        sent_kwargs.update(kw)
        return True, None

    monkeypatch.setattr(tr, "send_telegram", fake_send_telegram)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction="drop the Kafka bullet")

    assert ok is True
    assert applied_with["instructions"] == ["drop the Kafka bullet"]
    assert sent_kwargs["instructions_pending"] is False

    tailored = tr.load_tailored()
    assert tailored["1"]["instructions"] == ["drop the Kafka bullet"]
    assert tailored["1"]["base_tex_fields"] == {
        "skills_section": "base skills", "bullets": ["base bullet"], "projects": "base projects",
    }


def test_rebuild_one_flags_instructions_pending_when_llm_unavailable(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)
    db_path = tmp_path / "test.db"
    _make_empty_jobs_db(db_path)
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    monkeypatch.setattr(
        tr, "build_resume_fields",
        lambda job, jd_data, tight=False: ("base skills", ["base bullet"], "base projects", "focus", ["kw"]),
    )
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)
    monkeypatch.setattr(tr, "apply_instructions", lambda skills, bullets, projects, instructions: ((skills, bullets, projects), False))

    sent_kwargs = {}
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: sent_kwargs.update(kw) or (True, None))

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction="reword the top bullet")

    assert ok is True
    assert sent_kwargs["instructions_pending"] is True


def test_parse_rebuild_cli_args_job_and_reply_only():
    assert tr._parse_rebuild_cli_args(["5", "1100"]) == ("5", "1100", "fix", None)


def test_parse_rebuild_cli_args_with_mode_and_instruction():
    got = tr._parse_rebuild_cli_args(["5", "1100", "--mode", "update", "--instruction", "reword bullet 2"])
    assert got == ("5", "1100", "update", "reword bullet 2")


def test_parse_rebuild_cli_args_mode_only():
    got = tr._parse_rebuild_cli_args(["5", "1100", "--mode", "fix"])
    assert got == ("5", "1100", "fix", None)


def test_parse_rebuild_cli_args_too_few_args_raises():
    with pytest.raises(ValueError):
        tr._parse_rebuild_cli_args(["5"])
