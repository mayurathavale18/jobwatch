import copy
import json
import subprocess
import sys
from pathlib import Path
import pytest
from job_decision import decide, digest, extract_requirements


CANDIDATE = {"experience_years": 1.75, "country": "India", "facts": "Built Go APIs in production", "cash_basis": "base", "minimum_annual_cash": {"INR": 2500000, "USD": 35000}}


def case(jd="Requirements:\nAt least 1 year experience building Go APIs.\nThis role can hire in India.\nAnnual base salary USD 40000-50000."):
    return {"id": "a", "source_url": "https://example.test/posting", "state": {"title": "Backend Engineer", "location": "Remote", "jd": jd}, "human_review": {"skill_match": True, "skill_match_confirmed": True, "skill_evidence": "building Go APIs", "candidate_skill_evidence": "Built Go APIs in production", "location_eligible": True, "location_evidence": "This role can hire in India.", "annual_cash": {"minimum": 40000, "maximum": 50000, "currency": "USD", "period": "annual", "basis": "base"}, "salary_evidence": "Annual base salary USD 40000-50000."}}


def test_apply_requires_confirmed_skills_location_cash_and_no_stretch():
    result = decide(case(), CANDIDATE)
    assert result["decision"] == "apply"
    assert result["reasons"] == []
    assert result["shadow_only"]
    for key in ("skill_match_confirmed", "location_evidence", "salary_evidence"):
        job = case()
        del job["human_review"][key]
        assert decide(job, CANDIDATE)["decision"] == "review"


@pytest.mark.parametrize("years", [2, 4, 5, 8])
def test_experience_gap_is_review_and_exact_evidence_is_preserved(years):
    job = case()
    job["state"]["jd"] = job["state"]["jd"].replace("1 year", f"{years} years")
    result = decide(job, CANDIDATE)
    assert result["decision"] == "review"
    assert "experience_stretch" in result["reasons"]
    for claim in result["requirements"]["experience"]:
        assert claim["evidence"] in job["state"]["jd"]


def test_preferred_years_and_required_domain_remain_distinct():
    jd = "Ideally 5 years experience building services.\nRequirements:\n4+ years production Go experience.\n2 years maintaining authentication protocols experience."
    evidence = extract_requirements({"title": "Auth Engineer", "jd": jd})["experience"]
    assert [(e["minimum_years"], e["strength"]) for e in evidence] == [(5, "preferred"), (4, "required"), (2, "required")]
    assert "authentication protocols" in evidence[2]["evidence"]


def test_mixed_required_preferred_clauses_keep_both_years():
    evidence = extract_requirements({"title": "Engineer", "jd": "Required: 8 years experience, preferred: 10 years experience."})["experience"]
    assert [(e["minimum_years"], e["strength"]) for e in evidence] == [(8, "required"), (10, "preferred")]


def test_cash_interpretation_cannot_contradict_literal_quote():
    job = case()
    job["human_review"]["annual_cash"].update(minimum=20000, maximum=30000)
    with pytest.raises(ValueError, match="contradicts"):
        decide(job, CANDIDATE)


def test_unsupported_cash_format_abstains():
    job = case()
    job["state"]["jd"] += " Earn $40k-$50k yearly."
    job["human_review"]["salary_evidence"] = "Earn $40k-$50k yearly."
    assert decide(job, CANDIDATE)["decision"] == "review"


def test_preferred_stretch_and_ambiguous_cash_basis_need_review():
    job = case()
    job["state"]["jd"] += " Ideally 5 years experience."
    assert "preferred_experience_stretch" in decide(job, CANDIDATE)["reasons"]
    job = case()
    job["human_review"]["annual_cash"].update(minimum=10000, maximum=20000, basis="CTC")
    job["state"]["jd"] = job["state"]["jd"].replace("Annual base salary USD 40000-50000.", "Compensation including benefits USD 10000-20000.")
    job["human_review"]["salary_evidence"] = "Compensation including benefits USD 10000-20000."
    result = decide(job, CANDIDATE)
    assert result["decision"] == "review"
    assert "human_confirmed_cash_below_floor" not in result["reasons"]


def test_reviewed_interpretations_and_provenance_remain_auditable():
    job = case()
    job["source_sha256"] = "source snapshot hash"
    result = decide(job, CANDIDATE)
    assert result["human_review"] == job["human_review"]
    assert result["source_sha256"] == job["source_sha256"]
    assert result["requirements"]["location"][0] == {"source": "location_header", "evidence": "Remote"}


def test_cli_never_overwrites_inputs(tmp_path):
    cases = tmp_path / "cases.json"
    candidate = tmp_path / "candidate.json"
    cases.write_text(json.dumps([case()]), encoding="utf-8")
    candidate.write_text(json.dumps(CANDIDATE), encoding="utf-8")
    original = cases.read_bytes()
    process = subprocess.run([sys.executable, str(Path(__file__).with_name("job_decision.py")), str(cases), str(candidate), str(cases)], capture_output=True)
    assert process.returncode != 0
    assert cases.read_bytes() == original


def test_unknown_years_do_not_invent_a_requirement():
    job = case()
    job["state"]["jd"] = job["state"]["jd"].replace("At least 1 year", "Some")
    assert "experience_requirement_uncertain" in decide(job, CANDIDATE)["reasons"]
    assert extract_requirements({"title": "Engineer", "jd": "Company founded 5 years ago."})["experience"] == []
    assert extract_requirements({"title": "Engineer", "jd": "Required 1.5 years experience."})["experience"] == []


@pytest.mark.parametrize("title", ["Product Support Specialist", "Director Technical Accounting", "Lead Product Designer", "Deal Desk Manager"])
def test_occupation_is_title_anchored(title):
    job = case()
    job["state"]["title"] = title
    job["state"]["jd"] += " Work with software engineers on APIs and SQL."
    assert decide(job, CANDIDATE)["decision"] == "skip"


def test_global_remote_timezone_preference_is_evidence_not_permission():
    job = case()
    job["state"]["location"] = "United Kingdom"
    job["state"]["jd"] += " Role is remote globally. Prefer GMT to GMT+3."
    del job["human_review"]["location_eligible"]
    result = decide(job, CANDIDATE)
    assert result["decision"] == "review"
    assert any("GMT" in item["evidence"] for item in result["requirements"]["location"])


def test_explicit_reviewed_location_and_pay_mismatch_skip():
    job = case()
    job["human_review"]["location_eligible"] = False
    assert "human_confirmed_location_mismatch" in decide(job, CANDIDATE)["reasons"]
    job = case()
    job["human_review"]["annual_cash"].update(minimum=20000, maximum=30000)
    job["state"]["jd"] = job["state"]["jd"].replace("40000-50000", "20000-30000")
    job["human_review"]["salary_evidence"] = "Annual base salary USD 20000-30000."
    assert "human_confirmed_cash_below_floor" in decide(job, CANDIDATE)["reasons"]
    job["human_review"]["annual_cash"]["maximum"] = 40000
    job["state"]["jd"] = job["state"]["jd"].replace("20000-30000", "20000-40000")
    job["human_review"]["salary_evidence"] = "Annual base salary USD 20000-40000."
    assert decide(job, CANDIDATE)["decision"] == "review"


@pytest.mark.parametrize("change", ["hash", "evidence", "boolean", "nonfinite", "range"])
def test_invalid_claims_fail_closed(change):
    job = case()
    candidate = copy.deepcopy(CANDIDATE)
    if change == "hash":
        job["state_sha256"] = digest({"jd": "different"})
    elif change == "evidence":
        job["human_review"]["location_evidence"] = "Invented India permission"
    elif change == "boolean":
        job["human_review"]["skill_match_confirmed"] = "yes"
    elif change == "nonfinite":
        candidate["experience_years"] = float("nan")
    else:
        job["human_review"]["annual_cash"].update(minimum=50000, maximum=40000)
    with pytest.raises(ValueError):
        decide(job, candidate)
