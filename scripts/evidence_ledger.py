"""Retrieve exact candidate facts for explicitly selected requirements, offline."""
import argparse
import json
import re
from pathlib import Path
from rank_shadow import digest

LEVELS = {"production", "used", "familiar", "planned", "unknown"}
STOP = set("a an the and or in on for of to with using build building design designing implement implementing experienced experience strong background skills skill ability capable new core components systems system work working services service features feature".split())
ALIASES = {"golang": "go", "postgres": "postgresql", "k8s": "kubernetes", "ts": "typescript"}


def tokens(text):
    return {ALIASES.get(word, word) for word in re.findall(r"[a-z][a-z0-9+#]*", text.lower()) if word not in STOP}


def facts_chunks(profile, source_path):
    """Keep lines verbatim; inherit only explicit heading-level knowledge labels."""
    source_hash = digest(profile)
    headings = []
    chunks = []
    for line_number, line in enumerate(profile.splitlines(), 1):
        heading = re.match(r"^(#{1,6})\s+(.+)$", line)
        if heading:
            depth, title = len(heading[1]), heading[2]
            headings = [item for item in headings if item[0] < depth] + [(depth, title)]
            continue
        if not line.strip() or re.fullmatch(r"\s*[-*_]{3,}\s*", line):
            continue
        level = "unknown"
        for _, title in headings:
            match = re.fullmatch(r"\s*(production|used|familiar|planned|unknown)(?:\s+(?:skills|experience|knowledge))?\s*:?[ ]*", title, re.I)
            if match:
                level = match[1].lower()
        explicit = re.match(r"^\s*(?:[-*]\s+)?(?:\*\*)?(production|used|familiar|planned|unknown)(?:\*\*)?\s*:", line, re.I)
        if explicit:
            level = explicit[1].lower()
        limitations = [line] if re.search(r"\[FILL|\b(?:unknown|unconfirmed|not evidenced|not claimed|do not|never|limited|only|not independently)\b", line, re.I) else []
        chunks.append({"id": digest(f"{source_hash}:{line_number}:{line}"), "text": line,
                       "source_path": str(source_path), "source_sha256": source_hash,
                       "start_line": line_number, "headings": [title for _, title in headings],
                       "source_level": level, "unresolved": "[FILL" in line, "limitations": limitations})
    return chunks


def build_ledger(cases, profile, requirements, source_path):
    if not isinstance(cases, list) or not 1 <= len(cases) <= 1000 or not isinstance(requirements, list) or not 1 <= len(requirements) <= 1000:
        raise ValueError("Expected 1..1000 cases and requirement records")
    if not isinstance(profile, str) or not profile.strip() or len(profile) > 2_000_000:
        raise ValueError("Expected bounded nonempty facts")
    by_id = {}
    for case in cases:
        if not isinstance(case, dict) or not isinstance(case.get("id"), (str, int)) or isinstance(case["id"], bool) or not isinstance(case.get("state"), dict):
            raise ValueError("Malformed case")
        key = str(case["id"])
        state_hash = digest(json.dumps(case["state"], sort_keys=True))
        if key in by_id or case.get("state_sha256") != state_hash or not isinstance(case["state"].get("jd"), str):
            raise ValueError("Duplicate case or changed posting hash")
        by_id[key] = case
    facts = facts_chunks(profile, source_path)
    records, seen = [], set()
    for requirement in requirements:
        if not isinstance(requirement, dict) or not isinstance(requirement.get("id"), (str, int)) or isinstance(requirement["id"], bool):
            raise ValueError("Malformed requirement record")
        key = str(requirement["id"])
        if key in seen or key not in by_id:
            raise ValueError("Duplicate or unknown requirement id")
        seen.add(key)
        case = by_id[key]
        clauses = requirement.get("clauses")
        if not isinstance(clauses, list) or not 1 <= len(clauses) <= 100:
            raise ValueError("Expected 1..100 clauses")
        selected, seen_clauses = [], set()
        for clause in clauses:
            if not isinstance(clause, dict):
                raise ValueError("Malformed clause")
            evidence = clause.get("evidence")
            if not isinstance(evidence, str) or not evidence.strip() or len(evidence) > 20000 or evidence not in case["state"]["jd"] or evidence in seen_clauses:
                raise ValueError("Clause must be unique exact posting evidence")
            seen_clauses.add(evidence)
            capabilities = clause.get("capabilities", [evidence])
            if not isinstance(capabilities, list) or not 1 <= len(capabilities) <= 100 or any(not isinstance(claim, str) or not claim.strip() or claim not in evidence for claim in capabilities) or len(set(capabilities)) != len(capabilities):
                raise ValueError("Capabilities must be unique exact clause excerpts")
            comparisons = []
            for claim in capabilities:
                words = tokens(claim)
                matches = [(words & tokens(fact["text"]), fact) for fact in facts]
                matches = sorted((item for item in matches if item[0]), key=lambda item: (-len(item[0]), item[1]["start_line"]))[:3]
                comparisons.append({"claim": claim, "relation": "related_only" if matches else "no_candidates",
                    "candidates": [dict(fact, fact_id=fact["id"], overlap_tokens=sorted(overlap)) for overlap, fact in matches]})
            selected.append({"evidence": evidence, "capabilities": comparisons})
        records.append({"id": key, "state_sha256": case["state_sha256"], "profile_sha256": digest(profile), "clauses": selected})
    # shortcut: lexical overlap finds related text, use reviewed semantic retrieval only after benchmarking.
    return {"schema_version": 1, "advisory_only": True, "scope": "requirement_evidence_retrieval",
            "profile_sha256": digest(profile), "source_path": str(source_path), "facts": facts, "records": records}


def validate_ledger(ledger, cases, profile):
    """Recompute retrieval and every provenance field from frozen inputs."""
    if not isinstance(ledger, dict) or not isinstance(ledger.get("records"), list):
        raise ValueError("Malformed ledger")
    try:
        requirements = [{"id": record["id"], "clauses": [
            {"evidence": clause["evidence"], "capabilities": [item["claim"] for item in clause["capabilities"]]}
            for clause in record["clauses"]]} for record in ledger["records"]]
        expected = build_ledger(cases, profile, requirements, ledger["source_path"])
    except (KeyError, TypeError) as exc:
        raise ValueError("Malformed ledger") from exc
    if ledger != expected:
        raise ValueError("Ledger does not match frozen evidence")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for field in ("cases", "facts", "requirements", "out"):
        parser.add_argument("--" + field, type=Path, required=True)
    args = parser.parse_args()
    if any(args.out.resolve() == source.resolve() or (args.out.exists() and source.exists() and args.out.samefile(source)) for source in (args.cases, args.facts, args.requirements)):
        raise ValueError("Output cannot overwrite input")
    for path in (args.cases, args.facts, args.requirements):
        if path.stat().st_size > 20_000_000:
            raise ValueError("Input exceeds 20 MB")
    result = build_ledger(json.loads(args.cases.read_text(encoding="utf-8")), args.facts.read_text(encoding="utf-8"),
                          json.loads(args.requirements.read_text(encoding="utf-8")), args.facts.resolve())
    args.out.write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
