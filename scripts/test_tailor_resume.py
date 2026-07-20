"""Tests for scripts/tailor_resume.py. Run with: pytest scripts/test_tailor_resume.py -v"""
import sys
from pathlib import Path

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
