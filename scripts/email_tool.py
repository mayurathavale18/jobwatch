#!/usr/bin/env python3
"""Recruiter/referral email helper for the dashboard's compose UI.

Reads one JSON request on stdin, writes one JSON response on stdout, exit
code 0 even on handled failures (the response carries ok/error) so the Go
side only has to parse, never interpret exit codes. Actions:

  generate   {job_id, to_name?, instruction?}          -> {subject, body}
  revise     {subject, body, instruction, company?, title?} -> {subject, body}
  save_draft {job_id, to?, subject, body, draft_id?}   -> {draft_id, link}
  send       {job_id, draft_id}                        -> {}

Reuses tailor_resume's OpenCode + Gmail plumbing (same .env, same
gmail.compose scope -- compose covers create/update/send of drafts).
An empty "to" is allowed for save_draft: the draft lands in Gmail with a
blank To: field for Mayur to fill in manually.
"""
import json
import sqlite3
import sys
from datetime import datetime
from pathlib import Path
from urllib.request import Request, urlopen

import os


def load_dotenv(path):
    """Minimal `.env` loader (`export KEY=value` / `KEY=value` lines). The
    dashboard pod runs the Go binary directly with no env, unlike the cron
    wrappers which `source .env` -- and tailor_resume reads os.environ at
    import time, so this must run before that import. Existing env wins.
    """
    try:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
    except OSError:
        return
    for line in lines:
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, val = line.removeprefix("export ").partition("=")
        os.environ.setdefault(key.strip(), val.strip().strip('"').strip("'"))


load_dotenv(Path(__file__).resolve().parent.parent / ".env")
sys.path.insert(0, str(Path(__file__).resolve().parent))
import tailor_resume as tr  # noqa: E402


def get_job(job_id):
    conn = sqlite3.connect(tr.DB_PATH)
    conn.row_factory = sqlite3.Row
    try:
        row = conn.execute("SELECT * FROM jobs WHERE id = ?", (int(job_id),)).fetchone()
        return dict(row) if row else None
    finally:
        conn.close()


def set_email_fields(job_id, draft_id=None, to=None, sent_at=None):
    sets, args = [], []
    for col, val in (("email_draft_id", draft_id), ("email_to", to), ("email_sent_at", sent_at)):
        if val is not None:
            sets.append(f"{col} = ?")
            args.append(val)
    if not sets:
        return
    args.append(int(job_id))
    conn = sqlite3.connect(tr.DB_PATH)
    try:
        conn.execute(f"UPDATE jobs SET {', '.join(sets)} WHERE id = ?", args)
        conn.commit()
    finally:
        conn.close()


def generate(req):
    job = get_job(req["job_id"])
    if not job:
        return {"ok": False, "error": f"job {req['job_id']} not found"}
    jd_text, _unavailable = tr.fetch_jd_text_for_job(job)
    resume_text = tr.load_master_tex()
    to_name = req.get("to_name", "")
    instruction = req.get("instruction", "")

    user_content = (
        f"RECIPIENT: {to_name or 'the recruiter/hiring team (name unknown)'}\n\n"
        f"JOB DESCRIPTION:\n{jd_text}\n\nRESUME:\n{resume_text}"
    )
    if instruction:
        user_content += f"\n\nADDITIONAL CONTEXT FROM THE CANDIDATE:\n{instruction}"

    raw = tr.call_opencode(
        system_prompt=(
            "You write short, genuine-sounding emails from a software "
            "engineer job candidate to a recruiter or potential referrer "
            f"about a {job.get('title')} role at {job.get('company_name')}. "
            "Reference one or two concrete points from the job description "
            "and the candidate's resume that make them a strong fit -- do "
            "not invent any experience not present in the resume text. "
            "Mention that a tailored resume is attached. If additional "
            "context from the candidate is given, honor its tone/emphasis "
            "requests. Keep it under 150 words, no generic flattery. "
            "Respond with ONLY valid JSON, no markdown fences: "
            '{"subject": "...", "body": "..."}'
        ),
        user_content=user_content,
        model=tr.OUTREACH_EMAIL_MODEL,
        timeout=60,
    )
    return _parse_subject_body(raw)


def revise(req):
    raw = tr.call_opencode(
        system_prompt=(
            "You revise a job candidate's email per their instruction. "
            "Keep it professional, concrete, and under 150 words unless "
            "the instruction says otherwise. Never invent experience. "
            "Respond with ONLY valid JSON, no markdown fences: "
            '{"subject": "...", "body": "..."}'
        ),
        user_content=(
            f"CURRENT SUBJECT: {req.get('subject', '')}\n\n"
            f"CURRENT BODY:\n{req.get('body', '')}\n\n"
            f"INSTRUCTION: {req.get('instruction', '')}\n\n"
            f"(Role context: {req.get('title', '')} at {req.get('company', '')})"
        ),
        model=tr.OUTREACH_EMAIL_MODEL,
        timeout=60,
    )
    return _parse_subject_body(raw)


def _parse_subject_body(raw):
    if not raw:
        return {"ok": False, "error": "LLM unavailable or timed out"}
    try:
        parsed = json.loads(raw.strip().strip("`").removeprefix("json").strip())
        subject = str(parsed.get("subject", "")).strip()
        body = str(parsed.get("body", "")).strip()
        if subject and body:
            return {"ok": True, "subject": subject, "body": body}
    except (json.JSONDecodeError, AttributeError):
        pass
    return {"ok": False, "error": "LLM returned malformed JSON"}


def _draft_payload(to, subject, body, attachment_path):
    import base64
    from email.mime.application import MIMEApplication
    from email.mime.multipart import MIMEMultipart
    from email.mime.text import MIMEText

    msg = MIMEMultipart()
    if to:
        msg["to"] = to
    msg["subject"] = subject
    msg.attach(MIMEText(body, "plain"))
    if attachment_path and Path(attachment_path).exists():
        with open(attachment_path, "rb") as f:
            part = MIMEApplication(f.read(), _subtype="pdf")
        part.add_header("Content-Disposition", "attachment", filename=Path(attachment_path).name)
        msg.attach(part)
    raw = base64.urlsafe_b64encode(msg.as_bytes()).decode("utf-8")
    return json.dumps({"message": {"raw": raw}}).encode("utf-8")


def save_draft(req):
    job_id = req["job_id"]
    to = req.get("to", "").strip()
    subject = req.get("subject", "").strip()
    body = req.get("body", "").strip()
    if not subject or not body:
        return {"ok": False, "error": "subject and body required"}

    token = tr._gmail_access_token()
    if not token:
        return {"ok": False, "error": "Gmail not configured or token refresh failed"}

    # Attach the tailored resume when its PDF still exists on this
    # pod's filesystem (cron-pod PDFs are ephemeral -- fine to skip).
    attachment = None
    entry = tr.load_tailored().get(str(job_id)) or {}
    if entry.get("pdf_path") and Path(entry["pdf_path"]).exists():
        attachment = entry["pdf_path"]

    payload = _draft_payload(to, subject, body, attachment)
    draft_id = req.get("draft_id", "").strip()
    url = tr.GMAIL_DRAFTS_URL + (f"/{draft_id}" if draft_id else "")
    method = "PUT" if draft_id else "POST"
    try:
        r = Request(url, data=payload, method=method, headers={
            "Authorization": f"Bearer {token}", "Content-Type": "application/json"})
        with urlopen(r, timeout=30) as resp:
            data = json.loads(resp.read())
    except Exception as e:
        return {"ok": False, "error": str(e)}

    new_draft_id = data.get("id", draft_id)
    message_id = (data.get("message") or {}).get("id")
    link = f"https://mail.google.com/mail/u/0/#all/{message_id}" if message_id else None
    set_email_fields(job_id, draft_id=new_draft_id, to=to)
    return {"ok": True, "draft_id": new_draft_id, "link": link,
            "attached_resume": bool(attachment)}


def send(req):
    job_id = req["job_id"]
    draft_id = req.get("draft_id", "").strip()
    if not draft_id:
        return {"ok": False, "error": "draft_id required"}
    token = tr._gmail_access_token()
    if not token:
        return {"ok": False, "error": "Gmail not configured or token refresh failed"}
    try:
        r = Request(
            tr.GMAIL_DRAFTS_URL + "/send",
            data=json.dumps({"id": draft_id}).encode("utf-8"),
            method="POST",
            headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        )
        with urlopen(r, timeout=30) as resp:
            resp.read()
    except Exception as e:
        return {"ok": False, "error": str(e)}
    set_email_fields(job_id, sent_at=datetime.now().isoformat())
    return {"ok": True}


ACTIONS = {"generate": generate, "revise": revise, "save_draft": save_draft, "send": send}


def main():
    try:
        req = json.load(sys.stdin)
        action = ACTIONS.get(req.get("action", ""))
        if not action:
            resp = {"ok": False, "error": f"unknown action {req.get('action')!r}"}
        else:
            resp = action(req)
    except Exception as e:  # never crash: the Go side only parses stdout
        resp = {"ok": False, "error": str(e)}
    json.dump(resp, sys.stdout)


if __name__ == "__main__":
    main()
