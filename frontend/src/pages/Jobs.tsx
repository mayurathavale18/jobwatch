import { useEffect, useState } from 'react'
import { fetchJobs, patchJob, submitManualJob, triggerOutreach, type JobsResponse } from '../api'

function filtersFromLocation(): { status: string; provider: string; company: string; q: string; days: number } {
  const params = new URLSearchParams(window.location.search)
  return {
    status: params.get('status') ?? '',
    provider: params.get('provider') ?? '',
    company: params.get('company') ?? '',
    q: params.get('q') ?? '',
    days: Number(params.get('days')) || 0,
  }
}

function pageFromLocation(): number {
  const raw = Number(new URLSearchParams(window.location.search).get('page'))
  return raw > 1 ? raw : 1
}

export default function Jobs() {
  const [filters, setFilters] = useState(filtersFromLocation)
  const [page, setPage] = useState(pageFromLocation)
  const [data, setData] = useState<JobsResponse | null>(null)
  const [manualUrl, setManualUrl] = useState('')
  const [manualJdText, setManualJdText] = useState('')
  const [manualOutreachInstruction, setManualOutreachInstruction] = useState('')
  const [manualStatus, setManualStatus] = useState<string | null>(null)
  const [manualSubmitting, setManualSubmitting] = useState(false)
  const [outreachOverride, setOutreachOverride] = useState<Record<number, string>>({})
  const [outreachStatusMsg, setOutreachStatusMsg] = useState<Record<number, string>>({})

  // Typed as a minimal structural type (just the one method this handler
  // needs) rather than importing React.FormEvent -- this file has no
  // existing `import React` or `FormEvent` import to hang that off of,
  // and pulling one in for a single event handler isn't worth it.
  async function handleSubmitManualJob(e: { preventDefault: () => void }) {
    e.preventDefault()
    const url = manualUrl.trim()
    if (!url) return
    // Validation moved here from the native <input required type="url">
    // constraint: on some mobile browsers/layouts, a failing native
    // constraint cancels the submit event before this handler ever runs
    // and renders no visible bubble -- the tap just does nothing, with
    // no way to tell what went wrong. Explicit JS validation always
    // surfaces a result through manualStatus, on every platform.
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      setManualStatus('Enter a valid http(s) URL.')
      return
    }
    setManualSubmitting(true)
    setManualStatus(null)
    try {
      const result = await submitManualJob(url, manualJdText.trim(), manualOutreachInstruction.trim())
      setManualStatus(
        result.alreadyExisted
          ? `Already added — #J${result.id} (${result.company})`
          : `Added #J${result.id} — ${result.company} — resume incoming on Telegram`,
      )
      setManualUrl('')
      setManualJdText('')
      setManualOutreachInstruction('')
    } catch (err) {
      setManualStatus(`Failed to add: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setManualSubmitting(false)
    }
  }

  // Reads a dropped/picked .txt or .md file client-side and drops its
  // content into the JD-text textarea, overwriting whatever was there --
  // one JD-text source at a time, not appended.
  function handleJdFileChange(e: { target: { files: FileList | null } }) {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === 'string') setManualJdText(reader.result)
    }
    reader.readAsText(file)
  }

  useEffect(() => {
    const params = new URLSearchParams()
    if (filters.status) params.set('status', filters.status)
    if (filters.provider) params.set('provider', filters.provider)
    if (filters.company) params.set('company', filters.company)
    if (filters.q) params.set('q', filters.q)
    if (filters.days) params.set('days', String(filters.days))
    if (page > 1) params.set('page', String(page))
    const qs = params.toString()
    window.history.replaceState(null, '', qs ? `/?${qs}` : '/')

    fetchJobs({ ...filters, page }).then(setData).catch(console.error)
  }, [filters, page])

  function updateFilters(patch: Partial<{ status: string; provider: string; company: string; q: string; days: number }>) {
    setFilters((f) => ({ ...f, ...patch }))
    setPage(1)
  }

  function toggleStatus(status: string) {
    const current = filters.status ? filters.status.split(',') : []
    const next = current.includes(status)
      ? current.filter((s) => s !== status)
      : [...current, status]
    updateFilters({ status: next.join(',') })
  }

  async function updateStatus(id: number, status: string) {
    await patchJob(id, { status })
    setData((d) =>
      d ? { ...d, jobs: d.jobs.map((j) => (j.id === id ? { ...j, status } : j)) } : d,
    )
  }

  async function updateNotes(id: number, notes: string) {
    await patchJob(id, { notes })
  }

  async function handleTriggerOutreach(id: number) {
    setOutreachStatusMsg((m) => ({ ...m, [id]: 'Queued…' }))
    try {
      await triggerOutreach(id, outreachOverride[id]?.trim() || undefined)
      setOutreachStatusMsg((m) => ({ ...m, [id]: 'Queued — check Gmail Drafts / Telegram' }))
    } catch (err) {
      setOutreachStatusMsg((m) => ({ ...m, [id]: `Failed: ${err instanceof Error ? err.message : String(err)}` }))
    }
  }

  if (!data) return <p className="muted">Loading...</p>

  return (
    <>
      <form className="add-job-form" onSubmit={handleSubmitManualJob} noValidate>
        <div className="add-job">
          <input
            type="url"
            placeholder="Paste a job posting URL (Keka, Workday, anywhere)..."
            value={manualUrl}
            onChange={(e) => setManualUrl(e.target.value)}
          />
          <button type="submit" disabled={manualSubmitting}>
            {manualSubmitting ? 'Adding…' : 'Add & Tailor'}
          </button>
          {manualStatus && <span className="add-job-status">{manualStatus}</span>}
        </div>
        <div className="add-job-jd">
          <textarea
            placeholder="Paste JD text (optional) — used instead of scraping the link"
            value={manualJdText}
            onChange={(e) => setManualJdText(e.target.value)}
          />
          <input type="file" accept=".txt,.md" onChange={handleJdFileChange} />
        </div>
        <div className="add-job-outreach-instruction">
          <textarea
            placeholder="Outreach instructions (optional) — e.g. an email address to draft to, or notes on tone/what to mention"
            value={manualOutreachInstruction}
            onChange={(e) => setManualOutreachInstruction(e.target.value)}
          />
          <span className="hint">
            An email address in this text is used automatically. Free-text asks like "find HR's
            email" can't be fulfilled yet — Apollo's free tier can't look up people.
          </span>
        </div>
      </form>

      <div className="stats">
        <span>
          Total: <span className="count">{data.totalJobs}</span>
        </span>
        {Object.entries(data.statusCounts).map(([status, n]) => (
          <span key={status}>
            {status}: <span className="count">{n}</span>
          </span>
        ))}
        {data.lastPoll ? (
          <>
            <span>Last poll: {data.lastPoll.finishedAt}</span>
            <span>
              OK: {data.lastPoll.companiesOK} / Failed: {data.lastPoll.companiesFailed} / New:{' '}
              {data.lastPoll.newJobs}
            </span>
            {data.lastPoll.errors && <span className="errors">Errors: {data.lastPoll.errors}</span>}
          </>
        ) : (
          <span className="muted">No poll runs yet</span>
        )}
      </div>

      <div className="filters">
        <div className="status-pills">
          {data.statuses.map((s) => {
            const active = filters.status.split(',').includes(s)
            return (
              <button
                key={s}
                type="button"
                className={`status-pill${active ? ' active' : ''}`}
                onClick={() => toggleStatus(s)}
              >
                {s}
              </button>
            )
          })}
        </div>
        <select
          value={filters.provider}
          onChange={(e) => updateFilters({ provider: e.target.value })}
        >
          <option value="">All providers</option>
          {data.providers.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </select>
        <select
          value={filters.company}
          onChange={(e) => updateFilters({ company: e.target.value })}
        >
          <option value="">All companies</option>
          {data.companies.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
        <select
          value={String(filters.days)}
          onChange={(e) => updateFilters({ days: Number(e.target.value) })}
        >
          <option value="0">All time</option>
          <option value="1">Last 24h</option>
          <option value="7">Last 7 days</option>
          <option value="30">Last 30 days</option>
        </select>
        <input
          type="text"
          placeholder="Search title, company, location..."
          value={filters.q}
          onChange={(e) => updateFilters({ q: e.target.value })}
        />
        <a href="/">Reset</a>
      </div>

      <table>
        <thead>
          <tr>
            <th>First seen</th>
            <th>Company</th>
            <th>Title</th>
            <th>Location</th>
            <th>Fit</th>
            <th>Status</th>
            <th>Notes</th>
            <th>Outreach</th>
          </tr>
        </thead>
        <tbody>
          {data.jobs.length === 0 ? (
            <tr>
              <td colSpan={8} className="muted">
                No jobs match the current filters.
              </td>
            </tr>
          ) : (
            data.jobs.map((job) => (
              <tr key={job.id}>
                <td>{job.firstSeenAt}</td>
                <td>{job.companyName}</td>
                <td>
                  <a className="title-link" href={job.url} target="_blank" rel="noopener noreferrer">
                    {job.title}
                  </a>
                </td>
                <td>{job.location}</td>
                <td>
                  {job.fitScore != null ? (
                    <span
                      className="fit-badge"
                      title={job.workMode || undefined}
                      style={{ color: job.fitScore >= 70 ? 'var(--good, #2e7d32)' : job.fitScore >= 40 ? 'inherit' : 'var(--bad, #c62828)' }}
                    >
                      {job.fitScore}
                      {job.workMode ? ` · ${job.workMode}` : ''}
                    </span>
                  ) : (
                    <span className="muted">—</span>
                  )}
                </td>
                <td>
                  <select
                    className={`status-${job.status}`}
                    value={job.status}
                    onChange={(e) => updateStatus(job.id, e.target.value)}
                  >
                    {data.statuses.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>
                </td>
                <td>
                  <input
                    className="notes-input"
                    type="text"
                    defaultValue={job.notes}
                    onBlur={(e) => updateNotes(job.id, e.target.value)}
                  />
                </td>
                <td className="outreach-cell">
                  {job.outreachStatus === 'drafted' ? (
                    <span className="outreach-drafted">
                      Drafted — {job.founderName || job.founderEmail}
                    </span>
                  ) : (
                    <>
                      <input
                        type="email"
                        placeholder="Override founder email (optional)"
                        value={outreachOverride[job.id] ?? ''}
                        onChange={(e) => setOutreachOverride((m) => ({ ...m, [job.id]: e.target.value }))}
                      />
                      <button type="button" onClick={() => handleTriggerOutreach(job.id)}>
                        Draft outreach
                      </button>
                    </>
                  )}
                  {outreachStatusMsg[job.id] && <div className="outreach-status-msg">{outreachStatusMsg[job.id]}</div>}
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>

      {data.totalFiltered > data.pageSize && (
        <div className="pagination">
          <button type="button" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
            Prev
          </button>
          <span>
            Page {page} of {Math.max(1, Math.ceil(data.totalFiltered / data.pageSize))}
          </span>
          <button
            type="button"
            disabled={page >= Math.ceil(data.totalFiltered / data.pageSize)}
            onClick={() => setPage((p) => p + 1)}
          >
            Next
          </button>
        </div>
      )}
    </>
  )
}
