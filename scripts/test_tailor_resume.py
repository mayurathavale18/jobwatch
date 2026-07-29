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


def test_fetch_jd_generic_strips_lever_apply_suffix(monkeypatch):
    # Lever's "/apply" URL (what LinkedIn's "Apply" button links to, and
    # what jobwatch.mayurathavale.com users end up pasting) renders the
    # *application form*, not the JD -- scraping it grabs form boilerplate
    # ("do you have the legal right to work...") instead of responsibilities
    # /requirements. That boilerplate is long enough to pass the
    # MIN_USABLE_JD_CHARS check, so it silently poisons is_engineering_role
    # (the word "legal" trips the non-engineering blocklist) even for an
    # unambiguous "Backend Engineer" title. Fix: strip "/apply" so the real
    # JD page -- one URL away -- gets scraped instead.
    requested_urls = []
    fake_html = (
        "<html><body><main><h1>Backend Engineer</h1>"
        "<p>We are looking for a backend engineer with distributed systems "
        "and Kubernetes experience to join our platform team immediately.</p>"
        "</main></body></html>"
    ).encode("utf-8")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return fake_html

    def fake_urlopen(req, timeout=15):
        requested_urls.append(req.full_url)
        return FakeResponse()

    monkeypatch.setattr(tr, "urlopen", fake_urlopen)
    result = tr.fetch_jd_generic(
        "https://jobs.lever.co/portcast/1f6381eb-03dd-451a-a8cc-2c862cec3fe3/apply?lever-source=LinkedIn"
    )
    assert requested_urls == [
        "https://jobs.lever.co/portcast/1f6381eb-03dd-451a-a8cc-2c862cec3fe3"
    ]
    assert result is not None
    assert "Backend Engineer" in result["content_text"]


def test_fetch_jd_generic_leaves_non_lever_apply_urls_untouched(monkeypatch):
    requested_urls = []
    fake_html = b"<html><body><main><p>some content</p></main></body></html>"

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return fake_html

    def fake_urlopen(req, timeout=15):
        requested_urls.append(req.full_url)
        return FakeResponse()

    monkeypatch.setattr(tr, "urlopen", fake_urlopen)
    tr.fetch_jd_generic("https://example.com/careers/apply/123")
    assert requested_urls == ["https://example.com/careers/apply/123"]


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


def test_llm_judge_extracts_sector_field_when_present(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit.", "sector": "fintech"}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] == "fintech"


def test_llm_judge_sector_none_when_absent_from_response(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit."}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] is None


def test_llm_judge_sector_none_for_rule_based_fallback(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda *a, **k: None)
    result = tr.llm_judge("JD text about Go and PostgreSQL", "resume text", "Backend Engineer", "Acme")
    assert result["source"] == "rule_based"
    assert result["sector"] is None


def test_llm_judge_rejects_invalid_sector_value(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit.", "sector": "gaming"}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] is None


def test_llm_judge_sector_none_for_unhashable_sector_value(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit.", "sector": ["fintech", "web3"]}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] is None


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


def test_apply_instructions_strips_markdown_fences(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, **kwargs: "```json\n" + json.dumps({
            "skills_section": "\\techSkill{Languages}{Go, Python}",
            "bullets": ["Built a thing."],
            "projects": "\\resumeItem{A project.}",
        }) + "\n```",
    )
    (skills, bullets, projects), ok = tr.apply_instructions(
        "\\techSkill{Languages}{Go}", ["Built a thing."], "\\resumeItem{A project.}",
        ["mention Python too"],
    )
    assert ok is True
    assert skills == "\\techSkill{Languages}{Go, Python}"


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


def test_is_engineering_role_hr_hack_no_longer_needed():
    # "hr " (with a trailing space) was a hand-rolled workaround to avoid
    # matching inside unrelated words like "Chrome" -- confirm the
    # word-boundary version still correctly allows such titles.
    assert tr.is_engineering_role("Chrome Extension Engineer", "") is True


def test_is_engineering_role_still_blocks_hr_titles():
    assert tr.is_engineering_role("HR Business Partner", "") is False


def test_is_engineering_role_still_blocks_legal_titles():
    assert tr.is_engineering_role("Corporate Counsel", "") is False


def test_max_required_years_plus_pattern():
    assert tr.max_required_years("5+ years of experience required") == 5


def test_max_required_years_range_pattern_uses_lower_bound():
    assert tr.max_required_years("3-5 years of experience") == 3


def test_max_required_years_no_requirement_stated():
    assert tr.max_required_years("Great team, fast-paced environment.") is None


def test_max_required_years_picks_highest_stated_minimum():
    text = "2+ years with Python. 6+ years of overall professional experience."
    assert tr.max_required_years(text) == 6


def test_exceeds_experience_cap_true_above_cap():
    assert tr.exceeds_experience_cap("Requires 5+ years of experience.") is True


def test_exceeds_experience_cap_false_at_cap():
    assert tr.exceeds_experience_cap("Requires 3+ years of experience.") is False


def test_exceeds_experience_cap_false_when_unstated():
    assert tr.exceeds_experience_cap("Backend Engineer role.") is False


def test_process_job_skips_over_experience_role(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "fetch_greenhouse_job", lambda slug, gh_id: None)
    monkeypatch.setattr(
        tr, "fetch_jd_generic",
        lambda url: {
            "title": "", "company_name": "",
            "content_text": (
                "Backend Engineer role. We are looking for a strong software "
                "engineer to join our platform team. Requires 6+ years of "
                "professional experience building distributed systems at scale."
            ),
            "content_html": "", "location": "", "absolute_url": url,
        },
    )
    job = {"id": 3, "company_name": "Acme", "title": "Backend Engineer", "url": "https://example.com/3"}
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "skipped_over_experience"
    assert tailored["3"]["status"] == "ignored"


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


def test_process_job_uses_manual_jd_text_skips_fetch_jd_generic(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "output")
    monkeypatch.setattr(tr, "fetch_greenhouse_job", lambda slug, gh_id: None)

    def fail_if_called(url):
        raise AssertionError("fetch_jd_generic should not be called when manual_jd_text is present")
    monkeypatch.setattr(tr, "fetch_jd_generic", fail_if_called)
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    captured_jd_data = {}

    def fake_build_resume_fields(job, jd_data, tight=False):
        captured_jd_data.update(jd_data)
        return "skills", ["bullet"], "projects", "focus", ["kw"]
    monkeypatch.setattr(tr, "build_resume_fields", fake_build_resume_fields)

    manual_jd_text = (
        "We need a Backend Engineer to own our payments infrastructure, working "
        "across Go microservices, Kubernetes deployments, and PostgreSQL data "
        "stores serving millions of transactions daily."
    )
    job = {
        "id": 3, "company_name": "Acme", "title": "Backend Engineer",
        "url": "https://example.com/3",
        "manual_jd_text": manual_jd_text,
    }
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "sent"
    assert captured_jd_data["content_text"] == manual_jd_text


def test_process_job_whitespace_only_manual_jd_text_falls_through_to_scrape(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "output")
    monkeypatch.setattr(tr, "fetch_greenhouse_job", lambda slug, gh_id: None)

    fetch_jd_generic_called = {"value": False}

    def working_fetch_jd_generic(url):
        fetch_jd_generic_called["value"] = True
        return {
            "title": "", "company_name": "", "content_text": (
                "We need a Backend Engineer to own our payments infrastructure, working "
                "across Go microservices, Kubernetes deployments, and PostgreSQL data "
                "stores serving millions of transactions daily."
            ),
            "content_html": "", "location": "", "absolute_url": url,
        }
    monkeypatch.setattr(tr, "fetch_jd_generic", working_fetch_jd_generic)
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    job = {
        "id": 4, "company_name": "Acme", "title": "Backend Engineer",
        "url": "https://example.com/4",
        "manual_jd_text": "   \n  ",
    }
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "sent"
    assert fetch_jd_generic_called["value"] is True


def test_process_job_trusts_short_manual_jd_text_verbatim(monkeypatch, tmp_path):
    # Regression test: manual_jd_text under MIN_USABLE_JD_CHARS (150) used to
    # be discarded by the quality-floor check even though it should always be
    # trusted verbatim once present -- length isn't a signal of "unreliable
    # scrape" for text the user explicitly pasted in, unlike fetch_jd_generic's
    # output. A short manual_jd_text must still win over falling back to
    # title-only, and fetch_jd_generic must never be called.
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "output")
    monkeypatch.setattr(tr, "fetch_greenhouse_job", lambda slug, gh_id: None)

    def fail_if_called(url):
        raise AssertionError("fetch_jd_generic should not be called when manual_jd_text is present")
    monkeypatch.setattr(tr, "fetch_jd_generic", fail_if_called)
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    captured_jd_data = {}

    def fake_build_resume_fields(job, jd_data, tight=False):
        captured_jd_data.update(jd_data)
        return "skills", ["bullet"], "projects", "focus", ["kw"]
    monkeypatch.setattr(tr, "build_resume_fields", fake_build_resume_fields)

    short_manual_jd_text = "Backend Engineer, Go + Kubernetes, remote OK."  # well under 150 chars
    assert len(short_manual_jd_text) < tr.MIN_USABLE_JD_CHARS
    job = {
        "id": 5, "company_name": "Acme", "title": "Backend Engineer",
        "url": "https://example.com/5",
        "manual_jd_text": short_manual_jd_text,
    }
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "sent"
    assert captured_jd_data["content_text"] == short_manual_jd_text


def test_rebuild_one_uses_manual_jd_text_from_db(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)

    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    conn.execute("CREATE TABLE jobs (id INTEGER PRIMARY KEY, manual_jd_text TEXT NOT NULL DEFAULT '')")
    conn.execute(
        "INSERT INTO jobs (id, manual_jd_text) VALUES (1, ?)",
        ("We need a backend engineer with Go, Kubernetes, and PostgreSQL experience.",),
    )
    conn.commit()
    conn.close()
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    captured_jd_data = {}

    def fake_build_resume_fields(job, jd_data, tight=False):
        captured_jd_data.update(jd_data)
        return "skills", ["bullet"], "projects", "focus", ["kw"]

    monkeypatch.setattr(tr, "build_resume_fields", fake_build_resume_fields)
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction=None)

    assert ok is True
    assert captured_jd_data["content_text"] == "We need a backend engineer with Go, Kubernetes, and PostgreSQL experience."


def test_rebuild_one_whitespace_only_manual_jd_text_falls_through_to_title(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)

    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    conn.execute("CREATE TABLE jobs (id INTEGER PRIMARY KEY, manual_jd_text TEXT NOT NULL DEFAULT '')")
    conn.execute(
        "INSERT INTO jobs (id, manual_jd_text) VALUES (1, ?)",
        ("   \n  ",),
    )
    conn.commit()
    conn.close()
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    captured_jd_data = {}

    def fake_build_resume_fields(job, jd_data, tight=False):
        captured_jd_data.update(jd_data)
        return "skills", ["bullet"], "projects", "focus", ["kw"]

    monkeypatch.setattr(tr, "build_resume_fields", fake_build_resume_fields)
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction=None)

    assert ok is True
    assert captured_jd_data["content_text"] == "Backend Engineer"


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


def test_rebuild_one_update_mode_does_not_reapply_instructions_on_retry(monkeypatch, tmp_path):
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
            "status": "done", "base_tex_fields": cached_fields, "instructions": ["drop the Kafka bullet"],
        }
    })

    apply_calls = []

    def fake_apply_instructions(skills, bullets, projects, instructions):
        apply_calls.append(1)
        return (skills, bullets, projects), True

    monkeypatch.setattr(tr, "apply_instructions", fake_apply_instructions)
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")

    compile_calls = {"n": 0}

    def fake_compile_tex(tex_path):
        compile_calls["n"] += 1
        return (compile_calls["n"] >= 2, "")  # fail attempt 1, succeed attempt 2

    monkeypatch.setattr(tr, "compile_tex", fake_compile_tex)
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="update", instruction=None)

    assert ok is True
    assert len(apply_calls) == 1  # not re-called on the second (retry) attempt


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


def test_fetch_jd_text_for_job_uses_manual_jd_text_when_present():
    job = {
        "id": 1, "company_name": "Acme", "title": "Backend Engineer",
        "url": "https://example.com/job/1", "manual_jd_text": "We need Go and PostgreSQL experience.",
    }
    jd_text, jd_unavailable = tr.fetch_jd_text_for_job(job)
    assert jd_text == "We need Go and PostgreSQL experience."
    assert jd_unavailable is False


def test_fetch_jd_text_for_job_falls_back_to_title_only(monkeypatch):
    monkeypatch.setattr(tr, "fetch_jd_generic", lambda url: None)
    job = {"id": 2, "company_name": "Acme", "title": "Backend Engineer", "url": "https://example.com/job/2"}
    jd_text, jd_unavailable = tr.fetch_jd_text_for_job(job)
    assert jd_text == "Backend Engineer"
    assert jd_unavailable is True
