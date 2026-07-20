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
