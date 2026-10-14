import { useEffect, useState } from 'react'
import { emailAction, fetchApplications, importApplications, type PlatformApplication } from '../api'

export default function Applications() {
  const [apps, setApps] = useState<PlatformApplication[]>([])
  const [platform, setPlatform] = useState('instahyre')
  const [query, setQuery] = useState('')
  const [stage, setStage] = useState('')
  const [payload, setPayload] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { fetchApplications().then(setApps).catch(e => setMessage(String(e))) }, [])
  useEffect(() => {
    const timer = window.setInterval(() => { fetchApplications().then(setApps).catch(() => {}) }, 10000)
    return () => window.clearInterval(timer)
  }, [])
  const hasEmail = (a: PlatformApplication) => !!a.emailTo || !!a.contacts?.some(c => c.email)
  const state = (a: PlatformApplication) => a.emailSentAt ? 'Sent' : a.emailDraftId ? 'Gmail draft' : a.draftBody ? 'Draft prepared' : 'Needs draft'
  const filtered = apps.filter(a => (!platform || a.platform === platform) &&
    `${a.company} ${a.title} ${a.location} ${a.contacts?.map(c => `${c.name} ${c.email}`).join(' ')}`.toLowerCase().includes(query.toLowerCase()) &&
    (!stage || (stage === 'Missing email' ? !hasEmail(a) : state(a) === stage)))

  async function importSnapshot() {
    setBusy(true)
    try {
      const n = await importApplications(payload)
      setApps(await fetchApplications()); setPayload(''); setMessage(`Imported ${n} applications. Existing drafts and workflow status preserved.`)
    } catch (e) { setMessage(String(e)) } finally { setBusy(false) }
  }

  async function prepareDraft(a: PlatformApplication) {
    setBusy(true); setMessage(`Preparing ${a.company} — ${a.title}…`)
    try {
      const contact = a.contacts?.find(c => c.email)
      const draft = a.draftBody ? { ok: true, subject: a.draftSubject, body: a.draftBody } :
        await emailAction(a.jobId, { action: 'generate', toName: contact?.name || a.contacts?.[0]?.name })
      if (!draft.ok) throw new Error('error' in draft ? draft.error : 'Draft generation failed')
      const result = await emailAction(a.jobId, { action: 'save_draft', to: a.emailTo || contact?.email || '', subject: draft.subject, body: draft.body })
      if (!result.ok) throw new Error(result.error)
      setApps(await fetchApplications())
      setMessage(`Gmail draft saved for ${a.company} — ${a.title}${hasEmail(a) ? '' : '; recipient email still needed'}. ${result.attached_resume ? 'Resume attached.' : 'No resume attached; review before sending.'}`)
    } catch (e) { setMessage(String(e)) } finally { setBusy(false) }
  }

  async function queueDrafts() {
    setBusy(true)
    try {
      const res = await fetch('/api/applications/drafts', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({platform}) })
      if (!res.ok) throw new Error(await res.text())
      const data = await res.json()
      setMessage(`Queued ${data.queued} follow-up drafts for ${platform}. Known contact emails get Gmail drafts; missing emails keep drafts here for review. Progress and errors appear on the Cron tab. The worker must be running.`)
    } catch (e) { setMessage(String(e)) } finally { setBusy(false) }
  }

  return <div className="applications-page">
    <h2>Applications</h2>
    <p className="muted">Applied roles grouped by platform, with captured JDs, recruiter details and follow-up drafts.</p>
    <div className="stats">
      <span>{apps.filter(a => !platform || a.platform === platform).length} imported</span>
      <span>{apps.filter(a => (!platform || a.platform === platform) && hasEmail(a)).length} with contact email</span>
      <span>{apps.filter(a => (!platform || a.platform === platform) && a.draftBody && !a.emailSentAt).length} drafts prepared</span>
      <span>{apps.filter(a => (!platform || a.platform === platform) && a.emailDraftId && !a.emailSentAt).length} Gmail drafts</span>
    </div>
    <button type="button" disabled={busy || platform !== 'instahyre' || !apps.length} onClick={queueDrafts}>Prepare missing {platform || 'platform'} follow-ups</button>
    <div className="filters">
      <label>Platform <select value={platform} onChange={e => setPlatform(e.target.value)}>
        <option value="">All platforms</option>
        {[...new Set(['instahyre', ...apps.map(a => a.platform)])].map(p => <option key={p} value={p}>{p === 'instahyre' ? 'Instahyre' : p}</option>)}
      </select></label>
      <input aria-label="Search applications" placeholder="Company, role, location or recruiter" value={query} onChange={e => setQuery(e.target.value)} />
      <select aria-label="Draft status" value={stage} onChange={e => setStage(e.target.value)}>
        <option value="">All draft states</option>
        {['Missing email', 'Needs draft', 'Draft prepared', 'Gmail draft', 'Sent'].map(s => <option key={s}>{s}</option>)}
      </select>
    </div>
    {message && <p role="status">{message}</p>}
    <details className="application-import">
      <summary>Import browser capture</summary>
      <p className="muted">Import an Instahyre Applied jobs capture. Repeated imports update the same records. Sign-in and collection happen in the built-in browser when you ask to sync.</p>
      <label>Application capture JSON<textarea rows={5} value={payload} onChange={e => setPayload(e.target.value)} /></label>
      <button type="button" disabled={busy || !payload.trim()} onClick={importSnapshot}>Import applications</button>
    </details>
    <p className="muted">Showing {filtered.length} applications. A recruiter name alone does not provide an email address.</p>
    {filtered.map(a => <article className="application-card" key={a.jobId}>
      <div className="application-heading"><h3>{a.company} — {a.title}</h3><span>{a.platform} · {a.jobStatus}</span></div>
      <p>{a.location || 'Location not shown'} · Applied {a.appliedAt || 'date not shown'} · {a.metadata?.experience || 'Experience not shown'}</p>
      <p className="muted">{state(a)}{!hasEmail(a) && ' · Contact email needed'} · Last imported {new Date(a.importedAt).toLocaleString()}</p>
      <ul>{a.contacts?.map((c, i) => <li key={i}>{c.name || 'Hiring contact'}{c.role && ` · ${c.role}`}{c.email && ` · ${c.email}`}<small className="contact-evidence">{c.evidence}</small></li>)}</ul>
      <div className="email-actions">
        <a href={`/jobs/${a.jobId}/email`}>Review / compose follow-up</a>
        {!a.emailDraftId && !a.emailSentAt && <button type="button" disabled={busy} onClick={() => prepareDraft(a)}>Prepare Gmail draft</button>}
        {a.draftLink && !a.emailSentAt && <a href={a.draftLink} target="_blank" rel="noreferrer">Open Gmail draft</a>}
        <a href={a.url} target="_blank" rel="noreferrer">View on Instahyre</a>
      </div>
      <details><summary>Job description and application metadata</summary>
        <p>Source status: {a.sourceStatus} · Source identity: {a.sourceId}</p>
        <div className="captured-jd">{a.jd || 'JD not available in this capture.'}</div>
        {Object.entries(a.metadata || {}).map(([k,v]) => <p key={k}><strong>{k}:</strong> {v}</p>)}
      </details>
      {a.draftBody && <details><summary>Prepared email</summary><h4>{a.draftSubject}</h4><div className="captured-jd">{a.draftBody}</div></details>}
    </article>)}
    {!apps.length && <p>No applications imported yet. Open Instahyre in the built-in browser and sync your Applied jobs.</p>}
  </div>
}
