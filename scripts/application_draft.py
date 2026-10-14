#!/usr/bin/env python3
"""Prepare one imported application follow-up using the existing composer.

Missing contact emails keep the draft in Jobwatch for review. Known emails
also get an unsent Gmail draft. This command never sends mail.
"""
import json
import sqlite3
import sys

import email_tool as et


def prepare(job_id):
    application = et.get_application(job_id)
    job = et.get_job(job_id)
    if not application or not job:
        return {"ok": False, "error": "Imported application not found"}
    if job.get("email_sent_at") or job.get("email_draft_id"):
        return {"ok": True, "status": "already_processed"}
    contact = next((c for c in application.get("contacts") or [] if c.get("email")), {})
    to = job.get("email_to") or contact.get("email", "")
    with sqlite3.connect(et.tr.DB_PATH) as conn:
        subject, body = conn.execute("SELECT draft_subject,draft_body FROM platform_applications WHERE job_id=?", (job_id,)).fetchone()
    if not subject or not body:
        result = et.generate({"job_id": job_id, "to_name": contact.get("name", "")})
        if not result["ok"]:
            return result
        subject, body = result["subject"], result["body"]
    if not to:
        return {"ok": True, "status": "contact_needed"}
    result = et.save_draft({"job_id": job_id, "to": to, "subject": subject, "body": body})
    return {**result, "status": "gmail_draft" if result["ok"] else "failed"}


if __name__ == "__main__":
    try:
        # Model-helper diagnostics belong in worker logs, not structured output.
        out, sys.stdout = sys.stdout, sys.stderr
        result = prepare(int(sys.argv[1]))
        sys.stdout = out
        print(json.dumps(result))
        sys.exit(0 if result["ok"] else 1)
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
