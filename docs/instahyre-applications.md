# Instahyre application follow-ups

`/applications` separates already-applied roles by platform. The first supported
platform is Instahyre. Imported applications share the existing job email composer,
Gmail draft/save/send flow, and worker events queue. Imports do not run the discovery
poller, tailor a resume, send Telegram alerts, or send email.

## Capture and import

Use the signed-in Codex built-in browser to open Instahyre's **Applied jobs** tab.
Open every card on every page and wait for its full Job Description to render.
Capture the company, role, location, experience, JD, displayed recruiter name and
designation, Application Sent evidence, application date, and company metadata.
Only include an email when explicitly displayed in the application/JD; retain its
source text in `contacts[].evidence`. A displayed recruiter name is not an email.
The reusable DOM reader is `scripts/browser/instahyre.mjs`; use
`captureInstahyreApplication` inside the browser's read-only page evaluator.
It scans recruiter name/designation as well as the JD. Check it with
`node scripts/browser/instahyre.test.mjs`.

Import through **Import browser capture** or `POST /api/applications/import`:

```json
{"applications":[{
  "platform":"instahyre",
  "sourceId":"snapshot-v1:unique-fingerprint",
  "company":"Example",
  "title":"Backend Engineer",
  "location":"Bangalore",
  "url":"https://www.instahyre.com/candidate/opportunities/?matching=true&status=1",
  "appliedAt":"October 4, 2026",
  "sourceStatus":"Application Sent",
  "jd":"Full rendered JD text",
  "contacts":[{"name":"Recruiter name","email":"","role":"Talent Acquisition","evidence":"Recruiter displayed on the application"}],
  "metadata":{"experience":"1-3 Years","capturedAt":"2026-10-14T12:00:00Z"}
}]}
```

The batch is atomic. A repeated `(platform, sourceId)` updates the snapshot while
preserving Jobwatch status, drafts, recipient edits and sent history. Imported jobs
start as `applied`, so the normal `new`-job pipeline does not pick them up.

The current rendered Instahyre cards do not expose platform application IDs or
direct role links. The initial capture uses a 64-bit FNV-1a fingerprint of the JSON
array `[company,title,location,appliedAt,normalizedJD]`, where the JD's whitespace is
collapsed to single spaces. Check fingerprint uniqueness before import. Include the
JD because separate applications can share company, role, location and date.
**An edited JD changes this fingerprint:** review identity on later captures before
importing changed listings. Do not silently merge applications by company/title.
The source link opens Applied jobs, while Jobwatch retains the exact captured role.

Browser collection is an on-demand Codex session operation. The server does not
have access to the desktop browser session, and this feature does not schedule a
browser scraper or store Instahyre credentials/cookies.

## Drafting and review

**Prepare missing Instahyre follow-ups** calls `POST /api/applications/drafts`
with `{"platform":"instahyre"}`. The existing worker handles `application_draft`
events via `scripts/application_draft.py JOB_ID`. Pending/running events and jobs
with a Gmail draft or recorded send are skipped. Queue errors are visible on Cron.

The helper uses `email_tool.generate` with the observed application and captured
JD plus the resume and authoritative `resume/facts.md`. It persists the generated
subject/body locally. With a known contact email it also saves an unsent Gmail
draft; without one it stops at a local draft with **Contact email needed**.

The composer supports local save, manual edits, recipient selection, AI revision,
Gmail save/update, and the existing explicit send confirmation. Resume attachment
availability is checked at Gmail-save time; a draft must not claim an attachment
exists beforehand. This feature does not generate a new tailored resume.

Model/provider failures remain errors, not fabricated successful drafts. Gmail
credentials and model credentials are loaded from the existing server `.env`.
Never copy secrets into captures, fixtures or committed files.

## Initial capture, October 14, 2026

The browser showed 180 applied cards across six pages. All 180 full JDs and named
recruiters were captured. None exposed an email address in the rendered JD; two
addresses were displayed in recruiter designation fields (IXG and Myrik).
Eight company/title/location/date groups contained multiple distinct JDs; all 180
records were preserved. Private captures and SQLite backups are under ignored
`backups/` locally, not committed to Git.

Both configured production OpenCode models returned HTTP 403 during isolated draft
verification. That batch was stopped. The initial local drafts were instead
prepared from application details and verified resume facts using four evidence
categories, with that method recorded in each snapshot's metadata. Two Gmail
drafts were created through the connected Gmail account and independently verified
with the DRAFT label, correct recipients and content; the server's own Gmail
refresh token returned HTTP 400 and needs reauthorization. The other 178 drafts
need recipient emails. All need review before sending, and no email was sent.
This records the initial observation, not a guarantee of future provider availability.
