"""Local, evidence-backed shadow decisions. Never reads environment, databases or networks."""
import argparse
import hashlib
import json
import math
import re
from pathlib import Path


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode("utf-8")).hexdigest()


def extract_requirements(state):
    title, jd = state.get("title", ""), state.get("jd", "")
    if not isinstance(title, str) or not isinstance(jd, str):
        raise ValueError("Title and JD must be strings")
    nonengineering = re.search(r"\b(designer|account executive|accounting|deal desk|product support|marketing|legal|recruiter)\b", title, re.I)
    engineering = re.search(r"\b(engineer|developer|sre|software architect)\b", title, re.I)
    result = {"occupation": {"value": "non_engineering" if nonengineering else "engineering" if engineering else "unknown", "evidence": title, "source": "title"}, "experience": [], "location": [], "compensation": []}
    if state.get("location"):
        result["location"].append({"evidence": state["location"], "source": "location_header"})
    section = "unknown"
    # shortcut: conservative English clause extraction, use reviewed structured requirements for other formats.
    for match in re.finditer(r"[^\n!?]+?(?:[.!?](?=\s|$)|(?=\n|$))", jd):
        text = match.group().strip()
        if not text:
            continue
        if re.fullmatch(r"(?:preferred qualifications|nice to have|bonus points)\s*:?", text, re.I):
            section = "preferred"
        elif re.fullmatch(r"(?:requirements|required qualifications|what you bring|must have)\s*:?", text, re.I):
            section = "required"
        elif len(text) < 80 and text.endswith(":"):
            section = "unknown"
        for clause in re.split(r"(?<=[,;])\s*(?=(?:preferred|required|ideally|must|minimum|at least)\b)", text, flags=re.I):
            years_matches = list(re.finditer(r"(?<![\d.])\b(\d{1,2})(?:\s*[-–]\s*(\d{1,2}))?\s*\+?\s*years?\b", clause, re.I))
            if re.search(r"\bexperience\b", clause, re.I):
                preferred = bool(re.search(r"\b(ideally|prefer(?:red|ably)?|bonus|nice to have)\b", clause, re.I))
                required = bool(re.search(r"\b(required|must|at least|minimum)\b", clause, re.I))
                strength = "unknown" if preferred and required else "preferred" if preferred else "required" if required else section
                for years in years_matches:
                    result["experience"].append({"minimum_years": int(years[1]), "maximum_years": int(years[2]) if years[2] else None, "strength": strength, "evidence": clause})
        if re.search(r"\b(remote|globally|worldwide|reside|resident|located|authorization|time.?zone|GMT|UTC)\b", text, re.I):
            result["location"].append({"evidence": text, "source": "jd"})
        if re.search(r"\b(salary|compensation|base pay|annual pay|USD|INR|LPA)\b|[$₹]", text, re.I):
            result["compensation"].append({"evidence": text})
    return result


def _number(value, field):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
        raise ValueError(f"{field} must be a finite nonnegative number")
    return value


def decide(case, candidate):
    state = case["state"]
    state_hash = digest(state)
    if case.get("state_sha256", state_hash) != state_hash:
        raise ValueError("Case evidence hash does not match state")
    experience = _number(candidate["experience_years"], "experience_years")
    country = candidate["country"]
    if not isinstance(country, str) or not country.strip():
        raise ValueError("Candidate country is required")
    requirements = extract_requirements(state)
    review = case.get("human_review", {})
    if not isinstance(review, dict):
        raise ValueError("human_review must be an object")
    for field in ("skill_match", "skill_match_confirmed", "location_eligible"):
        if field in review and not isinstance(review[field], bool):
            raise ValueError(f"{field} must be boolean")
    for field in ("location_evidence", "salary_evidence", "skill_evidence"):
        if field in review and (not isinstance(review[field], str) or not review[field].strip() or review[field] not in state.get("jd", "")):
            raise ValueError(f"{field} must be an exact JD substring")
    if "candidate_skill_evidence" in review and (not isinstance(review["candidate_skill_evidence"], str) or not review["candidate_skill_evidence"].strip() or review["candidate_skill_evidence"] not in candidate.get("facts", "")):
        raise ValueError("candidate_skill_evidence must be an exact candidate facts substring")
    reasons, hard = [], []
    occupation = requirements["occupation"]["value"]
    if occupation == "non_engineering":
        hard.append("non_engineering_title")
    elif occupation == "unknown":
        reasons.append("occupation_unknown")
    # Location headers are retained but cannot establish permission to hire in the candidate country.
    location = review.get("location_eligible") if review.get("location_evidence") else None
    if location is False:
        hard.append("human_confirmed_location_mismatch")
    elif location is None:
        reasons.append("location_eligibility_unconfirmed")
    required = [item for item in requirements["experience"] if item["strength"] == "required"]
    if any(item["minimum_years"] > experience for item in required):
        reasons.append("experience_stretch")
    if any(item["minimum_years"] > experience for item in requirements["experience"] if item["strength"] == "preferred"):
        reasons.append("preferred_experience_stretch")
    if not requirements["experience"] or any(item["strength"] == "unknown" for item in requirements["experience"]):
        reasons.append("experience_requirement_uncertain")
    salary = review.get("annual_cash")
    if salary is None or not review.get("salary_evidence"):
        reasons.append("annual_cash_unconfirmed")
    else:
        if not isinstance(salary, dict):
            raise ValueError("annual_cash must be an object")
        low, high = _number(salary["minimum"], "annual_cash.minimum"), _number(salary["maximum"], "annual_cash.maximum")
        if low > high:
            raise ValueError("Annual cash range is reversed")
        literal = re.fullmatch(r"Annual\s+(base|fixed)\s+(?:salary|pay)\s+(USD|INR)\s+(\d+(?:,\d{3})*)\s*[-–]\s*(\d+(?:,\d{3})*)\.?", review["salary_evidence"], re.I)
        if literal and (literal[1].lower() != salary.get("basis") or literal[2].upper() != salary.get("currency") or int(literal[3].replace(",", "")) != low or int(literal[4].replace(",", "")) != high or salary.get("period") != "annual"):
            raise ValueError("Structured annual cash contradicts its literal evidence")
        basis_confirmed = bool(literal) and salary.get("period") == "annual" and salary.get("basis") in {"base", "fixed"} and salary.get("basis") == candidate.get("cash_basis")
        if not basis_confirmed:
            reasons.append("compensation_period_or_basis_unconfirmed")
        threshold = candidate.get("minimum_annual_cash", {}).get(salary.get("currency"))
        if threshold is None:
            reasons.append("compensation_currency_unconfirmed")
        else:
            threshold = _number(threshold, "minimum_annual_cash")
            if high < threshold and basis_confirmed:
                hard.append("human_confirmed_cash_below_floor")
            elif low < threshold:
                reasons.append("compensation_range_crosses_floor")
    if review.get("skill_match_confirmed") is not True:
        reasons.append("skill_match_unconfirmed")
    elif review.get("skill_match") is False:
        hard.append("human_confirmed_skill_mismatch")
    elif review.get("skill_match") is not True:
        reasons.append("skill_match_unconfirmed")
    elif not review.get("skill_evidence") or not review.get("candidate_skill_evidence"):
        reasons.append("skill_evidence_unconfirmed")
    result = {"id": str(case["id"]), "state_sha256": state_hash, "candidate_sha256": digest(candidate), "human_review_sha256": digest(review), "source_url": case.get("source_url", state.get("url")), "requirements": requirements, "decision": "skip" if hard else "review" if reasons else "apply", "reasons": hard + reasons, "shadow_only": True}
    for field in ("source_sha256", "provenance", "fetched_at_utc"):
        if field in case:
            result[field] = case[field]
    result["human_review"] = review
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cases", type=Path)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    if args.output.resolve() in {args.cases.resolve(), args.candidate.resolve()}:
        parser.error("Output must not overwrite cases or candidate input")
    cases = json.loads(args.cases.read_text(encoding="utf-8"))
    candidate = json.loads(args.candidate.read_text(encoding="utf-8"))
    ids = [str(case["id"]) for case in cases]
    if len(ids) != len(set(ids)):
        raise ValueError("Duplicate case id")
    results = [decide(case, candidate) for case in cases]
    args.output.write_text(json.dumps(results, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
