import { useEffect, useState } from 'react'
import { fetchJobs, patchJob, submitManualJob, type JobsResponse } from '../api'

function filtersFromLocation(): { status: string; company: string; q: string } {
  const params = new URLSearchParams(window.location.search)
  return {
    status: params.get('status') ?? '',
    company: params.get('company') ?? '',
    q: params.get('q') ?? '',
  }
}

export default function Jobs() {
  const [filters, setFilters] = useState(filtersFromLocation)
  const [data, setData] = useState<JobsResponse | null>(null)
  const [manualUrl, setManualUrl] = useState('')
  const [manualStatus, setManualStatus] = useState<string | null>(null)
  const [manualSubmitting, setManualSubmitting] = useState(false)

  // Typed as a minimal structural type (just the one method this handler
  // needs) rather than importing React.FormEvent -- this file has no
  // existing `import React` or `FormEvent` import to hang that off of,
  // and pulling one in for a single event handler isn't worth it.
  async function handleSubmitManualJob(e: { preventDefault: () => void }) {
    e.preventDefault()
    if (!manualUrl.trim()) return
    setManualSubmitting(true)
    setManualStatus(null)
    try {
      const result = await submitManualJob(manualUrl.trim())
      setManualStatus(
        result.alreadyExisted
          ? `Already added — #J${result.id} (${result.company})`
          : `Added #J${result.id} — ${result.company} — resume incoming on Telegram`,
      )
      setManualUrl('')
    } catch (err) {
      setManualStatus(`Failed to add: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setManualSubmitting(false)
    }
  }

  useEffect(() => {
    const params = new URLSearchParams()
    if (filters.status) params.set('status', filters.status)
    if (filters.company) params.set('company', filters.company)
    if (filters.q) params.set('q', filters.q)
    const qs = params.toString()
    window.history.replaceState(null, '', qs ? `/?${qs}` : '/')

    fetchJobs(filters).then(setData).catch(console.error)
  }, [filters])

  async function updateStatus(id: number, status: string) {
    await patchJob(id, { status })
    setData((d) =>
      d ? { ...d, jobs: d.jobs.map((j) => (j.id === id ? { ...j, status } : j)) } : d,
    )
  }

  async function updateNotes(id: number, notes: string) {
    await patchJob(id, { notes })
  }

  if (!data) return <p className="muted">Loading...</p>

  return (
    <>
      <form className="add-job" onSubmit={handleSubmitManualJob}>
        <input
          type="url"
          placeholder="Paste a job posting URL (Keka, Workday, anywhere)..."
          value={manualUrl}
          onChange={(e) => setManualUrl(e.target.value)}
          required
        />
        <button type="submit" disabled={manualSubmitting}>
          {manualSubmitting ? 'Adding…' : 'Add & Tailor'}
        </button>
        {manualStatus && <span className="add-job-status">{manualStatus}</span>}
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
        <select
          value={filters.status}
          onChange={(e) => setFilters((f) => ({ ...f, status: e.target.value }))}
        >
          <option value="">All statuses</option>
          {data.statuses.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <select
          value={filters.company}
          onChange={(e) => setFilters((f) => ({ ...f, company: e.target.value }))}
        >
          <option value="">All companies</option>
          {data.companies.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
        <input
          type="text"
          placeholder="Search title..."
          value={filters.q}
          onChange={(e) => setFilters((f) => ({ ...f, q: e.target.value }))}
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
            <th>Status</th>
            <th>Notes</th>
          </tr>
        </thead>
        <tbody>
          {data.jobs.length === 0 ? (
            <tr>
              <td colSpan={6} className="muted">
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
              </tr>
            ))
          )}
        </tbody>
      </table>
    </>
  )
}
