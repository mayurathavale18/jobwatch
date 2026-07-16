import { useEffect, useState } from 'react'
import { fetchCronStatuses, triggerCronJob, type CronJobStatus } from '../api'

function formatLastRun(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export default function Cron() {
  const [jobs, setJobs] = useState<CronJobStatus[] | null>(null)
  const [triggering, setTriggering] = useState<string | null>(null)
  const [triggered, setTriggered] = useState<string | null>(null)

  function refresh() {
    fetchCronStatuses().then(setJobs).catch(console.error)
  }

  useEffect(refresh, [])

  async function runNow(name: string) {
    setTriggering(name)
    setTriggered(null)
    try {
      await triggerCronJob(name)
      setTriggered(name)
      // Fast jobs (poll, tg-sync, dashboard-watchdog) usually finish within
      // a few seconds; slow ones (tailor-resume, weekly-backup) won't be
      // done yet, but the status row will still show "running" accurately
      // once loadCronStatuses picks up whatever it wrote so far.
      setTimeout(refresh, 3000)
    } catch (err) {
      console.error(err)
    } finally {
      setTriggering(null)
    }
  }

  if (!jobs) return <p className="muted">Loading...</p>

  return (
    <table>
      <thead>
        <tr>
          <th>Job</th>
          <th>Schedule</th>
          <th>Status</th>
          <th>Last run</th>
          <th>Detail</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {jobs.map((job) => (
          <tr key={job.Name}>
            <td>{job.Name}</td>
            <td className="muted">{job.Schedule}</td>
            <td>
              {job.HasRun ? (
                <span className={`badge badge-${job.Status}`}>{job.Status}</span>
              ) : (
                <span className="badge badge-unknown">never run</span>
              )}
            </td>
            <td className={job.Stale ? 'stale' : ''}>
              {job.HasRun ? (
                <>
                  {formatLastRun(job.LastRun)}
                  {job.Stale && ' (overdue)'}
                </>
              ) : (
                <span className="muted">—</span>
              )}
            </td>
            <td className="detail">{job.Detail || <span className="muted">—</span>}</td>
            <td>
              <button
                type="button"
                disabled={triggering === job.Name}
                onClick={() => runNow(job.Name)}
              >
                {triggering === job.Name ? 'Running…' : triggered === job.Name ? 'Triggered ✓' : 'Run now'}
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
