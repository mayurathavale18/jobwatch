import { useEffect, useState } from 'react'
import { emailAction, fetchJob, fetchReferrals, type Connection, type Job } from '../api'

export default function Email({ jobId }: { jobId: number }) {
  const [job, setJob] = useState<Job | null>(null)
  const [referrals, setReferrals] = useState<Connection[]>([])
  const [to, setTo] = useState('')
  const [toName, setToName] = useState('')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [instruction, setInstruction] = useState('')
  const [draftId, setDraftId] = useState('')
  const [draftLink, setDraftLink] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [msg, setMsg] = useState<string | null>(null)

  useEffect(() => {
    fetchJob(jobId)
      .then((j) => {
        setJob(j)
        setTo(j.emailTo || j.founderEmail || '')
        setDraftId(j.emailDraftId || '')
      })
      .catch((e) => setMsg(String(e)))
    fetchReferrals(jobId).then(setReferrals).catch(() => setReferrals([]))
  }, [jobId])

  async function run(label: string, fn: () => Promise<void>) {
    setBusy(label)
    setMsg(null)
    try {
      await fn()
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  const generate = () =>
    run('Generating…', async () => {
      const r = await emailAction(jobId, { action: 'generate', toName, instruction })
      if (!r.ok) throw new Error(r.error)
      setSubject(r.subject ?? '')
      setBody(r.body ?? '')
      setInstruction('')
    })

  const revise = () =>
    run('Revising…', async () => {
      const r = await emailAction(jobId, { action: 'revise', subject, body, instruction })
      if (!r.ok) throw new Error(r.error)
      setSubject(r.subject ?? subject)
      setBody(r.body ?? body)
      setInstruction('')
    })

  const saveDraft = () =>
    run('Saving draft…', async () => {
      const r = await emailAction(jobId, { action: 'save_draft', to, subject, body, draftId })
      if (!r.ok) throw new Error(r.error)
      setDraftId(r.draft_id ?? '')
      setDraftLink(r.link ?? '')
      setMsg(
        `Draft ${draftId ? 'updated' : 'saved'} in Gmail${r.attached_resume ? ' with resume attached' : ' (no resume PDF on server — attach manually)'}${to ? '' : ' — To: is blank, fill it in Gmail'}.`,
      )
    })

  const send = () => {
    if (!to) {
      setMsg('Add a recipient before sending (or send from Gmail after filling To:).')
      return
    }
    if (!window.confirm(`Send this email to ${to}?`)) return
    run('Sending…', async () => {
      // Always save first so what's sent is exactly what's on screen.
      const saved = await emailAction(jobId, { action: 'save_draft', to, subject, body, draftId })
      if (!saved.ok) throw new Error(saved.error)
      const r = await emailAction(jobId, { action: 'send', draftId: saved.draft_id })
      if (!r.ok) throw new Error(r.error)
      setDraftId('')
      setMsg(`Sent to ${to}.`)
      setJob((j) => (j ? { ...j, emailSentAt: new Date().toISOString() } : j))
    })
  }

  function useReferral(c: Connection) {
    setTo(c.email)
    setToName(`${c.firstName} ${c.lastName}`.trim())
    setInstruction(
      `This is a referral request to ${c.firstName}, a LinkedIn connection who works at ${c.company} as ${c.position}. Ask politely whether they'd be open to referring me.`,
    )
  }

  if (!job) return <p className="muted">{msg ?? 'Loading...'}</p>

  const disabled = busy !== null

  return (
    <div className="email-page">
      <p>
        <a href="/">← Jobs</a>
      </p>
      <h2>
        {job.companyName} — {job.title}
      </h2>
      <p className="muted">
        {job.location}
        {job.fitScore != null && ` · fit ${job.fitScore}${job.workMode ? ` · ${job.workMode}` : ''}`} ·{' '}
        <a href={job.url} target="_blank" rel="noopener noreferrer">
          posting
        </a>
        {job.emailSentAt && ` · sent ${job.emailSentAt}`}
      </p>

      <section className="referrals">
        <h3>Referral routes ({referrals.length})</h3>
        {referrals.length === 0 ? (
          <p className="muted">
            No imported connections at {job.companyName}. Import your LinkedIn Connections.csv on the{' '}
            <a href="/connections">Connections</a> tab.
          </p>
        ) : (
          <ul>
            {referrals.map((c) => (
              <li key={c.linkedinUrl || `${c.firstName}${c.lastName}`}>
                <a href={c.linkedinUrl} target="_blank" rel="noopener noreferrer">
                  {c.firstName} {c.lastName}
                </a>{' '}
                <span className="muted">
                  {c.position} @ {c.company}
                  {c.email ? ` · ${c.email}` : ' · no email shared — message on LinkedIn'}
                </span>{' '}
                <button type="button" disabled={disabled} onClick={() => useReferral(c)}>
                  Draft referral ask
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="compose">
        <h3>Compose</h3>
        <label>
          To
          <input
            type="email"
            placeholder="Recipient email (leave blank to fill in Gmail later)"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </label>
        <label>
          Recipient name
          <input type="text" placeholder="Optional" value={toName} onChange={(e) => setToName(e.target.value)} />
        </label>
        <label>
          Subject
          <input type="text" value={subject} onChange={(e) => setSubject(e.target.value)} />
        </label>
        <label>
          Body
          <textarea rows={14} value={body} onChange={(e) => setBody(e.target.value)} />
        </label>

        <label>
          AI instruction
          <textarea
            rows={3}
            placeholder={
              body
                ? 'e.g. "shorter", "mention the LangGraph copilot", "make the subject punchier"'
                : 'Optional context for the first draft, e.g. "recruiter reached out on LinkedIn"'
            }
            value={instruction}
            onChange={(e) => setInstruction(e.target.value)}
          />
        </label>

        <div className="email-actions">
          <button type="button" disabled={disabled} onClick={generate}>
            {body ? 'Regenerate' : 'Generate with AI'}
          </button>
          <button type="button" disabled={disabled || !body || !instruction.trim()} onClick={revise}>
            Revise with AI
          </button>
          <button type="button" disabled={disabled || !subject || !body} onClick={saveDraft}>
            {draftId ? 'Update Gmail draft' : 'Save Gmail draft'}
          </button>
          <button type="button" disabled={disabled || !subject || !body} onClick={send}>
            Send
          </button>
          {busy && <span className="muted">{busy}</span>}
        </div>
        {msg && <p className="email-msg">{msg}</p>}
        {draftLink && (
          <p>
            <a href={draftLink} target="_blank" rel="noopener noreferrer">
              Open draft in Gmail
            </a>
          </p>
        )}
      </section>
    </div>
  )
}
