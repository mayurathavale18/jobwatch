import { useState, type FormEvent } from 'react'
import { logInstahyreApplication } from '../api'

export default function InstahyreApplication({ onLogged }: { onLogged: () => void }) {
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [jobId, setJobId] = useState<number>()

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    setBusy(true)
    setMessage('')
    setJobId(undefined)
    try {
      const job = await logInstahyreApplication(new FormData(form))
      setJobId(job.id)
      setMessage(job.alreadyExisted ? 'Application already logged. Check the worker queue for draft status.' : 'Application logged. Gmail draft queued.')
      form.reset()
      onLogged()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <details className="add-job-form">
      <summary>Log an Instahyre application</summary>
      <form onSubmit={submit}>
        <p>Use this after applying. Jobwatch prepares a Gmail draft for you to review and send.</p>
        <div className="add-job">
          <label>Instahyre posting URL <input name="url" type="url" required /></label>
          <label>Company <input name="company" maxLength={200} required /></label>
          <label>Role <input name="title" maxLength={200} required /></label>
        </div>
        <div className="add-job-jd">
          <label htmlFor="instahyre-jd">Job description</label>
          <textarea id="instahyre-jd" name="jd" maxLength={60000} required />
        </div>
        <div className="add-job">
          <label>Recruiter or hiring manager name <input name="recipientName" maxLength={200} /></label>
          <label>Verified work email <input name="recipientEmail" type="email" /></label>
          <label>Relevant work sample URL <input name="workLink" type="url" maxLength={2048} /></label>
        </div>
        <p className="hint">If the posting has no verified email, the draft’s To field stays blank.</p>
        <div className="add-job">
          <label>PDF used to apply <input name="resume" type="file" accept="application/pdf,.pdf" required /></label>
          <button type="submit" disabled={busy}>{busy ? 'Logging…' : 'Log and prepare draft'}</button>
        </div>
        <p role="status">{message} {jobId && <a href={`/jobs/${jobId}/email`}>Open application</a>}</p>
      </form>
    </details>
  )
}
