"""Retrieve exact candidate facts for explicitly selected requirements, offline."""
import argparse
import json
import re
from pathlib import Path
from rank_shadow import digest

LEVELS = {"production", "used", "familiar", "planned", "unknown"}
STOP = set("a an the and or in on for of to with using build building design designing implement implementing experienced experience strong background skills skill ability capable new core components systems system work working services service features feature".split())
ALIASES = {"golang": "go", "postgres": "postgresql", "k8s": "kubernetes", "ts": "typescript",
           "auth": "authentication", "authn": "authentication", "authz": "authorization",
           "agents": "agent", "queues": "queue", "databases": "database", "requests": "request",
           "models": "model", "integrations": "integration", "migrations": "migration",
           "indexes": "index", "hooks": "hook", "protocols": "protocol", "clusters": "cluster"}


def tokens(text):
    return {ALIASES.get(word, word) for word in re.findall(r"[a-z][a-z0-9+#]*", text.lower()) if word not in STOP}


def facts_chunks(profile, source_path):
    """Keep lines verbatim; inherit only explicit heading-level knowledge labels."""
    source_hash = digest(profile)
    headings = []
    chunks = []
    lines = profile.splitlines()
    raw_lines = profile.splitlines(keepends=True)
    for line_number, line in enumerate(lines, 1):
        heading = re.match(r"^(#{1,6})\s+(.+)$", line)
        if heading:
            depth, title = len(heading[1]), heading[2]
            headings = [item for item in headings if item[0] < depth] + [(depth, title)]
            continue
        if (not line.strip() or re.fullmatch(r"\s*[-*_]{3,}\s*", line)
                or re.fullmatch(r"\s*\*\*[^*]+\*\*\s*", line)):
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
    by_line = {chunk["start_line"]: chunk for chunk in chunks}
    def boundary(number):
        line = lines[number - 1]
        return number not in by_line or re.fullmatch(r"\s*\*\*[^*]+\*\*\s*", line) is not None
    for chunk in chunks:
        number = chunk["start_line"]
        start = end = number
        is_bullet = re.match(r"^\s*[-*]\s+", chunk["text"]) is not None
        # Nested evidence keeps ancestor scope, including planned/familiar qualifiers.
        if is_bullet:
            indent = len(chunk["text"]) - len(chunk["text"].lstrip())
            cursor = number - 1
            while indent and cursor >= 1 and not boundary(cursor):
                preceding = lines[cursor - 1]
                parent = re.match(r"^(\s*)[-*]\s+", preceding)
                if parent and len(parent[1]) < indent:
                    start = cursor
                    indent = len(parent[1])
                cursor -= 1
        while start > 1 and not boundary(start - 1):
            preceding = lines[start - 2]
            if is_bullet:
                break
            if re.match(r"^\s*[-*]\s+", preceding):
                if lines[start - 1][:1].isspace():
                    start -= 1
                    is_bullet = True
                break
            start -= 1
        while end < len(lines) and not boundary(end + 1):
            following = lines[end]
            following_bullet = re.match(r"^(\s*)[-*]\s+", following)
            parent_indent = len(lines[start - 1]) - len(lines[start - 1].lstrip())
            if (following_bullet and (not is_bullet or len(following_bullet[1]) <= parent_indent)) or (is_bullet and not following[:1].isspace()):
                break
            end += 1
        full_start, full_end = start, end
        ancestors = [by_line[index] for index in range(start, number) if re.match(r"^\s*[-*]\s+", lines[index - 1])]
        restricted = [fact["source_level"] for fact in ancestors if fact["source_level"] in {"planned", "familiar"}]
        if restricted:
            chunk["source_level"] = "planned" if "planned" in restricted else "familiar"
        start, end = max(start, number - 3), min(end, number + 4)
        while sum(len(raw_lines[index - 1]) for index in range(start, end + 1)) > 4000 and start < end:
            if number - start > end - number:
                start += 1
            else:
                end -= 1
        context = [by_line[index] for index in range(start, end + 1)]
        context_text = "".join(raw_lines[start - 1:end]).rstrip("\r\n")
        chunk.update(context_text=context_text if len(context_text) <= 4000 else "",
                     context_start_line=start, context_end_line=end,
                     context_complete=start == full_start and end == full_end and len(chunk["text"]) <= 4000,
                     context_lines=[{field: fact[field] for field in ("start_line", "text", "source_level", "unresolved", "limitations")} for fact in context],
                     context_unresolved=any(fact["unresolved"] for fact in context),
                     context_limitations=[text for fact in context for text in fact["limitations"]],
                     context_requires_review=any(fact["source_level"] in {"familiar", "planned"} or re.search(
                         r"\b(?:familiar only|not production|not implemented|not shipped|planned only|not hands-on|concepts only)\b",
                         fact["text"], re.I) for fact in context))
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
    fact_tokens = [(tokens(fact["context_text"]), tokens(fact["text"]), fact) for fact in facts]
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
                ranked = sorted(((words & indexed, words & primary, fact) for indexed, primary, fact in fact_tokens if words & indexed),
                                key=lambda item: (-len(item[0]), -len(item[1]), item[2]["start_line"]))
                matches, seen_contexts = [], set()
                for overlap, _, fact in ranked:
                    context_key = (fact["context_start_line"], fact["context_end_line"])
                    if context_key not in seen_contexts:
                        matches.append((overlap, fact))
                        seen_contexts.add(context_key)
                    if len(matches) == 3:
                        break
                comparisons.append({"claim": claim, "relation": "related_only" if matches else "no_candidates",
                    "candidates": [dict(fact, fact_id=fact["id"], overlap_tokens=sorted(overlap)) for overlap, fact in matches]})
            selected.append({"evidence": evidence, "capabilities": comparisons})
        records.append({"id": key, "state_sha256": case["state_sha256"], "profile_sha256": digest(profile), "clauses": selected})
    # shortcut: lexical overlap finds related text, use reviewed semantic retrieval only after benchmarking.
    return {"schema_version": 2, "advisory_only": True, "scope": "requirement_evidence_retrieval",
            "profile_sha256": digest(profile), "source_path": str(source_path), "facts": facts, "records": records}


def validate_ledger(ledger, cases, profile):
    """Recompute retrieval and every provenance field from frozen inputs."""
    if not isinstance(ledger, dict) or not isinstance(ledger.get("records"), list):
        raise ValueError("Malformed ledger")
    if ledger.get("schema_version") != 2:
        raise ValueError("Ledger schema 2 required; regenerate a fresh output without modifying frozen schema 1 artifacts")
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
    if args.out.exists():
        raise ValueError("Output already exists; choose a fresh path to preserve frozen artifacts")
    for path in (args.cases, args.facts, args.requirements):
        if path.stat().st_size > 20_000_000:
            raise ValueError("Input exceeds 20 MB")
    result = build_ledger(json.loads(args.cases.read_text(encoding="utf-8")), args.facts.read_text(encoding="utf-8"),
                          json.loads(args.requirements.read_text(encoding="utf-8")), args.facts.resolve())
    args.out.write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
