import { useEffect, useState } from "react";
import {
  fetchCronStatuses,
  fetchEvents,
  triggerCronJob,
  type CronJobStatus,
  type QueueEvent,
} from "../api";

function formatLastRun(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export default function Cron() {
  const [jobs, setJobs] = useState<CronJobStatus[] | null>(null);
  const [triggering, setTriggering] = useState<string | null>(null);
  const [triggered, setTriggered] = useState<string | null>(null);
  const [events, setEvents] = useState<QueueEvent[]>([]);

  function refresh() {
    fetchCronStatuses().then(setJobs).catch(console.error);
    fetchEvents().then(setEvents).catch(console.error);
  }

  useEffect(() => {
    refresh();
    // Queued work (Telegram replies, run-now, dashboard actions) moves in
    // seconds; poll so status changes show without a manual reload.
    const t = setInterval(
      () => fetchEvents().then(setEvents).catch(console.error),
      3000,
    );
    return () => clearInterval(t);
  }, []);

  async function runNow(name: string) {
    setTriggering(name);
    setTriggered(null);
    try {
      await triggerCronJob(name);
      setTriggered(name);
      // Fast jobs (poll) usually finish within
      // a few seconds; slow ones (tailor-resume, weekly-backup) won't be
      // done yet, but the status row will still show "running" accurately
      // once loadCronStatuses picks up whatever it wrote so far.
      setTimeout(refresh, 3000);
    } catch (err) {
      console.error(err);
    } finally {
      setTriggering(null);
    }
  }

  if (!jobs) return <p className="muted">Loading...</p>;

  return (
    <>
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
                  <span className={`badge badge-${job.Status}`}>
                    {job.Status}
                  </span>
                ) : (
                  <span className="badge badge-unknown">never run</span>
                )}
              </td>
              <td className={job.Stale ? "stale" : ""}>
                {job.HasRun ? (
                  <>
                    {formatLastRun(job.LastRun)}
                    {job.Stale && " (overdue)"}
                  </>
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
              <td className="detail">
                {job.Detail || <span className="muted">—</span>}
              </td>
              <td>
                <button
                  type="button"
                  disabled={triggering === job.Name}
                  onClick={() => runNow(job.Name)}
                >
                  {triggering === job.Name
                    ? "Running…"
                    : triggered === job.Name
                      ? "Triggered ✓"
                      : "Run now"}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h3>Event queue</h3>
      <p className="muted">
        Telegram replies and dashboard actions, run by the jobwatch-worker pod.
        Newest first.
      </p>
      <table>
        <thead>
          <tr>
            <th>#</th>
            <th>Kind</th>
            <th>Status</th>
            <th>Queued</th>
            <th>Updated</th>
            <th>Detail</th>
          </tr>
        </thead>
        <tbody>
          {events.length === 0 ? (
            <tr>
              <td colSpan={6} className="muted">
                No events yet.
              </td>
            </tr>
          ) : (
            events.map((e) => (
              <tr key={e.id}>
                <td>{e.id}</td>
                <td>{e.kind}</td>
                <td>
                  <span
                    className={`badge badge-${e.status === "done" ? "OK" : e.status === "failed" ? "FAIL" : "SKIP"}`}
                  >
                    {e.status}
                    {e.attempts > 1 ? ` ×${e.attempts}` : ""}
                  </span>
                </td>
                <td>{formatLastRun(e.createdAt)}</td>
                <td>{formatLastRun(e.updatedAt)}</td>
                <td className="detail">
                  {e.error || (e.kind === "telegram_update" ? "" : e.payload)}
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </>
  );
}
