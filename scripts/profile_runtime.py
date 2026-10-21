"""Load validated Go profile exports once per action; never read legacy catalogs."""
from contextlib import contextmanager
from contextvars import ContextVar
from functools import wraps
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent
_snapshot = ContextVar("jobwatch_profile", default=None)


class ProfileError(ValueError):
    pass


def load_snapshot():
    binary = os.environ.get("JOBWATCH_BINARY", str(ROOT / "bin" / ("jobwatch.exe" if os.name == "nt" else "jobwatch")))
    db = os.environ.get("JOBWATCH_DB_PATH", str(ROOT / "jobwatch.db"))
    try:
        result = subprocess.run([binary, "profile", "export", "-db", db],
                                cwd=os.environ.get("JOBWATCH_WORKDIR", str(ROOT)),
                                capture_output=True, text=True, encoding="utf-8", timeout=15)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise ProfileError("Cannot load shared profile. Build jobwatch and import a profile first.") from exc
    if result.returncode:
        raise ProfileError("Cannot load shared profile. Check the selected database and run jobwatch profile import -apply.")
    return json.loads(result.stdout)


@contextmanager
def bind(snapshot):
    token = _snapshot.set(snapshot)
    try:
        yield snapshot
    finally:
        _snapshot.reset(token)


def action(function):
    @wraps(function)
    def wrapped(*args, **kwargs):
        if _snapshot.get() is not None:
            return function(*args, **kwargs)
        with bind(load_snapshot()):
            return function(*args, **kwargs)
    return wrapped


def snapshot():
    value = _snapshot.get()
    if value is None:
        raise ProfileError("No profile snapshot is bound to this action.")
    return value


def data():
    return snapshot()["profile"]


def stamp():
    content = json.dumps(data(), sort_keys=True, separators=(",", ":"), ensure_ascii=True)
    return {"profile_revision": snapshot()["revision"], "profile_sha256": hashlib.sha256(content.encode()).hexdigest()}


def matches(entry):
    return all(entry.get(key) == value for key, value in stamp().items())


def require_identity(resume=False):
    identity = data().get("identity", {})
    if not identity.get("name", "").strip() or (resume and not identity.get("email", "").strip()):
        raise ProfileError("Profile incomplete: candidate name" + (" and email" if resume else "") + " required.")
    template = data().get("resume", {}).get("template", "default")
    if resume and template not in ("", "default"):
        raise ProfileError("Only the default resume template is currently supported.")
    if resume and not (skills() or any(statements(item) for item in data().get("background", {}).get("experience", []))
                       or any(item["status"] == "confirmed" and statements(item) for item in data().get("background", {}).get("projects", []))):
        raise ProfileError("Profile incomplete: add candidate skills or confirmed background evidence before generating a resume.")
    return identity


def evidence():
    return {item["id"]: item for item in data().get("background", {}).get("evidence", [])}


def statements(item):
    facts = evidence()
    return [facts[key] for key in item.get("evidenceIds", []) if facts[key]["status"] == "confirmed"]


def skills():
    facts = evidence()
    return [skill for skill in data().get("background", {}).get("skills", [])
            if all(facts[key]["status"] == "confirmed" for key in skill.get("evidenceIds", []))]


def keyword_level(keyword):
    matches = [skill["level"] for skill in skills()
               if keyword.lower() in [term.lower() for term in [skill["name"], *skill.get("aliases", [])]]]
    # Conflicting declarations must not upgrade a skill into a direct claim.
    return {"used": "direct", "familiar": "hedged_familiar", "adjacent": "hedged_adjacent"}.get(
        matches[0] if len(set(matches)) == 1 else None, "fabrication_risk")


def candidate_text():
    require_identity()
    background = data().get("background", {})
    approved = dict(background)
    approved["evidence"] = [item for item in evidence().values() if item["status"] == "confirmed"]
    approved["skills"] = skills()
    approved["projects"] = [item for item in background.get("projects", []) if item["status"] == "confirmed"]
    # Filter references too: unconfirmed evidence IDs should not be prompt hints.
    ids = {item["id"] for item in approved["evidence"]}
    for key in ("skills", "experience", "projects"):
        approved[key] = [{**item, "evidenceIds": [ref for ref in item.get("evidenceIds", []) if ref in ids]}
                         for item in approved.get(key, [])]
    return json.dumps({"identity": data().get("identity", {}), "background": approved}, ensure_ascii=False)


def outreach_context():
    settings = data().get("outreach", {})
    return json.dumps({"candidate": json.loads(candidate_text()),
                       "signature": settings.get("signature") or data()["identity"]["name"],
                       "writing_instructions": settings.get("instructions", "")}, ensure_ascii=False)


def filename_prefix():
    import re
    name = require_identity(resume=True)["name"]
    return re.sub(r"[^a-z0-9]+", "_", name.lower()).strip("_") or "candidate"


def evidence_text():
    """Keep advisory evidence retrieval line-based and separate from skill lists."""
    lines = ["# Candidate-confirmed evidence"]
    for fact in evidence().values():
        if fact["status"] == "confirmed":
            lines.extend([fact["text"], "Source: " + fact["source"], ""])
    return "\n".join(lines)
