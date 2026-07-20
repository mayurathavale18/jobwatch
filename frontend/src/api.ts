export interface Job {
  id: number
  companySlug: string
  companyName: string
  title: string
  location: string
  url: string
  postedAt: string
  firstSeenAt: string
  status: string
  notes: string
}

export interface PollRun {
  finishedAt: string
  companiesOK: number
  companiesFailed: number
  newJobs: number
  errors: string
}

export interface JobsResponse {
  jobs: Job[]
  companies: string[]
  statusCounts: Record<string, number>
  totalJobs: number
  lastPoll: PollRun | null
  statuses: string[]
}

export interface CronJobStatus {
  Name: string
  Schedule: string
  HasRun: boolean
  LastRun: string
  Status: string
  Detail: string
  Stale: boolean
}

export interface JobFilters {
  status?: string
  company?: string
  q?: string
}

export async function fetchJobs(filters: JobFilters): Promise<JobsResponse> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.company) params.set('company', filters.company)
  if (filters.q) params.set('q', filters.q)
  const qs = params.toString()
  const res = await fetch(`/api/jobs${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`GET /api/jobs: ${res.status}`)
  return res.json()
}

export async function patchJob(id: number, patch: { status?: string; notes?: string }): Promise<void> {
  const res = await fetch(`/api/jobs/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!res.ok) throw new Error(`PATCH /api/jobs/${id}: ${res.status}`)
}

export async function fetchCronStatuses(): Promise<CronJobStatus[]> {
  const res = await fetch('/api/cron')
  if (!res.ok) throw new Error(`GET /api/cron: ${res.status}`)
  return res.json()
}

export async function triggerCronJob(name: string): Promise<void> {
  const res = await fetch(`/api/cron/${name}/run`, { method: 'POST' })
  if (!res.ok) throw new Error(`POST /api/cron/${name}/run: ${res.status}`)
}

export interface ManualJobResponse {
  id: number
  alreadyExisted: boolean
  company: string
  title: string
}

export async function submitManualJob(url: string): Promise<ManualJobResponse> {
  const res = await fetch('/api/jobs/manual', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  })
  if (!res.ok) throw new Error(`POST /api/jobs/manual: ${res.status}`)
  return res.json()
}
