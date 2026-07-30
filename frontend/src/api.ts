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
  outreachStatus: string
  founderName: string
  founderEmail: string
  outreachDraftedAt: string
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
  providers: string[]
  statusCounts: Record<string, number>
  totalJobs: number
  lastPoll: PollRun | null
  statuses: string[]
  page: number
  pageSize: number
  totalFiltered: number
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
  provider?: string
  company?: string
  q?: string
  days?: number
  page?: number
}

export async function fetchJobs(filters: JobFilters): Promise<JobsResponse> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.provider) params.set('provider', filters.provider)
  if (filters.company) params.set('company', filters.company)
  if (filters.q) params.set('q', filters.q)
  if (filters.days) params.set('days', String(filters.days))
  if (filters.page && filters.page > 1) params.set('page', String(filters.page))
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

export async function submitManualJob(
  url: string,
  jdText?: string,
  outreachInstruction?: string,
): Promise<ManualJobResponse> {
  const res = await fetch('/api/jobs/manual', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      url,
      ...(jdText ? { jdText } : {}),
      ...(outreachInstruction ? { outreachInstruction } : {}),
    }),
  })
  if (!res.ok) throw new Error(`POST /api/jobs/manual: ${res.status}`)
  return res.json()
}

export async function triggerOutreach(id: number, founderEmail?: string): Promise<void> {
  const res = await fetch(`/api/jobs/${id}/outreach`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(founderEmail ? { founderEmail } : {}),
  })
  if (!res.ok) throw new Error(`POST /api/jobs/${id}/outreach: ${res.status}`)
}
