// Only fields exposed by this editor are described here. Object spreads retain
// stored settings that do not have dashboard controls yet. Go owns validation.
export type Value = string | number | boolean | string[] | Row[] | undefined
export interface Row {
  [key: string]: Value
  id?: string
}
export type BackgroundKey = 'experience' | 'education' | 'skills' | 'projects' | 'evidence'
export interface CandidateProfile {
  schemaVersion: number
  identity: Row & { links?: Row[] } // links are edited separately
  background: Partial<Record<BackgroundKey, Row[]>>
  search: {
    preferences: Row & { workAuthorization?: Row[]; compensation?: Row[] }
    maxRequiredExperienceYears?: number
    [key: string]: unknown
  }
  outreach: Row
  [key: string]: unknown
}
export interface Snapshot {
  revision: number
  updatedAt?: string
  profile: CandidateProfile
}
export interface Field {
  key: string
  label: string
  kind?: 'textarea' | 'lines' | 'number' | 'month' | 'email' | 'url' | 'boolean' | 'evidence'
  options?: string[]
  hint?: string
}
export const status = ['needs_review', 'confirmed', 'planned']
export const backgroundFields: Record<BackgroundKey, Field[]> = {
  experience: [
    { key: 'company', label: 'Company' },
    { key: 'title', label: 'Title' },
    { key: 'location', label: 'Location' },
    { key: 'startDate', label: 'Start month', kind: 'month' },
    {
      key: 'endDate',
      label: 'End month',
      kind: 'month',
      hint: 'Leave blank for a current role.',
    },
    { key: 'evidenceIds', label: 'Supporting evidence', kind: 'evidence' },
  ],
  education: [
    { key: 'institution', label: 'Institution' },
    { key: 'qualification', label: 'Qualification' },
    { key: 'startDate', label: 'Start month', kind: 'month' },
    { key: 'endDate', label: 'End month', kind: 'month' },
  ],
  skills: [
    { key: 'name', label: 'Skill' },
    { key: 'category', label: 'Category' },
    {
      key: 'level',
      label: 'Experience level',
      options: ['used', 'familiar', 'adjacent'],
    },
    {
      key: 'aliases',
      label: 'Other names',
      kind: 'lines',
      hint: 'One name per line.',
    },
    { key: 'evidenceIds', label: 'Supporting evidence', kind: 'evidence' },
  ],
  projects: [
    { key: 'name', label: 'Project name' },
    { key: 'url', label: 'Project URL', kind: 'url' },
    { key: 'status', label: 'Status', options: status },
    { key: 'evidenceIds', label: 'Supporting evidence', kind: 'evidence' },
  ],
  evidence: [
    { key: 'text', label: 'Fact or achievement', kind: 'textarea' },
    {
      key: 'source',
      label: 'Source',
      kind: 'textarea',
      hint: 'Where this came from, or when you confirmed it.',
    },
    { key: 'status', label: 'Status', options: status },
    { key: 'tags', label: 'Tags', kind: 'lines', hint: 'One tag per line.' },
  ],
}
export function emptySnapshot(): Snapshot {
  return {
    revision: 0,
    profile: {
      schemaVersion: 1,
      identity: {},
      background: {},
      search: { filters: {}, preferences: {} },
      resume: {},
      outreach: {},
      automation: {},
    },
  }
}
export function newRow(section: string): Row {
  if (section === 'links') return { label: '', url: '' }
  if (section === 'compensation') return { market: '', currency: '', period: 'annual', basis: 'fixed' }
  if (section === 'workAuthorization') return { country: '' }
  return {
    id: `${section}-${crypto.randomUUID()}`,
    ...(section === 'skills'
      ? { name: '', level: 'familiar' }
      : section === 'evidence' || section === 'projects'
        ? { status: 'needs_review' }
        : {}),
  }
}
export function evidenceUsers(background: CandidateProfile['background'], id: string): string[] {
  return (['experience', 'skills', 'projects'] as const).flatMap((key) =>
    (background[key] ?? [])
      .filter((row) => (row.evidenceIds as string[] | undefined)?.includes(id))
      .map((row) => String(row.name || row.company || row.title)),
  )
}
export function parseValue(field: Field, text: string): Value {
  if (field.kind === 'boolean') return text === '' ? undefined : text === 'true'
  if (field.kind === 'number') return text === '' ? undefined : Number(text)
  // Keep whitespace while typing; trim list entries only at the save boundary.
  if (field.kind === 'lines') return text.split('\n')
  return text
}
export function prepareSave(snapshot: Snapshot): Snapshot {
  const saved = structuredClone(snapshot)
  const clean = (values: Value): Value =>
    Array.isArray(values) ? (values as string[]).map((value) => value.trim()).filter(Boolean) : values
  for (const row of saved.profile.background.skills ?? []) if (row.aliases) row.aliases = clean(row.aliases)
  for (const row of saved.profile.background.evidence ?? []) if (row.tags) row.tags = clean(row.tags)
  for (const key of ['workModes', 'companyTypes']) {
    if (saved.profile.search.preferences[key])
      saved.profile.search.preferences[key] = clean(saved.profile.search.preferences[key])
  }
  return JSON.parse(JSON.stringify(saved, (_key, value) => checkedNumber(value)))
}
function checkedNumber(value: unknown): unknown {
  if (typeof value === 'number' && !Number.isSafeInteger(value)) {
    throw new Error(
      'A number is outside the dashboard’s supported whole-number range. Use the CLI to preserve larger values.',
    )
  }
  return value
}
export function friendlyError(message: string): string {
  return message
    .replace(/profile\.background\.(\w+)\[(\d+)\]/g, (_, section, index) => `${section} entry ${Number(index) + 1}`)
    .replace(/profile\.search\.preferences\./g, 'Preferences: ')
    .replace(/\bprofile\.(?=\w)/g, '')
    .replace(/evidenceIds/g, 'supporting evidence')
    .replace(/needs_review/g, 'needs review')
}
export class ProfileRequestError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}
export async function profileRequest(snapshot?: Snapshot): Promise<Snapshot | null> {
  const response = await fetch(
    '/api/profile',
    snapshot
      ? {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(prepareSave(snapshot)),
        }
      : { cache: 'no-store' },
  )
  if (!snapshot && response.status === 404) return null
  if (!response.ok) throw new ProfileRequestError(response.status, friendlyError((await response.text()).trim()))
  return JSON.parse(await response.text(), (_key, value) => checkedNumber(value))
}
