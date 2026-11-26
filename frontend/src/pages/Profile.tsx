import { useEffect, useRef, useState } from 'react'
import type { BackgroundKey, Field, Row, Snapshot, Value } from '../profileForm'
import {
  backgroundFields,
  emptySnapshot,
  evidenceUsers,
  newRow,
  parseValue,
  profileRequest,
  ProfileRequestError,
} from '../profileForm'
import './Profile.css'

const sections = [
  'Identity',
  'Experience',
  'Education',
  'Skills',
  'Projects',
  'Evidence',
  'Preferences',
  'Outreach',
] as const
type Section = (typeof sections)[number]
const identityFields: Field[] = [
  { key: 'name', label: 'Full name' },
  { key: 'email', label: 'Email', kind: 'email' },
  { key: 'phone', label: 'Phone' },
  { key: 'location', label: 'Location' },
]
const preferenceFields: Field[] = [
  {
    key: 'workModes',
    label: 'Work modes, in preference order',
    kind: 'lines',
    hint: 'One per line: remote, hybrid or onsite.',
  },
  { key: 'willingToRelocate', label: 'Willing to relocate', kind: 'boolean' },
  {
    key: 'companyTypes',
    label: 'Company preferences',
    kind: 'lines',
    hint: 'One per line, in preference order.',
  },
  {
    key: 'notes',
    label: 'Search notes',
    kind: 'textarea',
    hint: 'Used for job-fit assessment. Keep offer and availability details here rather than in achievements.',
  },
]
const outreachFields: Field[] = [
  { key: 'signature', label: 'Email signature', kind: 'textarea' },
  { key: 'instructions', label: 'Writing instructions', kind: 'textarea' },
  {
    key: 'autoDraft',
    label: 'Automatic outreach drafts',
    kind: 'boolean',
    hint: 'Uses the existing sector, company-size and contact checks. This does not enable automatic sending.',
  },
  {
    key: 'maxEmployees',
    label: 'Maximum company employees',
    kind: 'number',
    hint: 'Leave blank to use the current default (20).',
  },
]
const hints: Partial<Record<Section, string>> = {
  Experience: 'Link each role to the evidence that supports its resume bullets.',
  Education: 'Dates use month and year. If a date is approximate, say so in the qualification.',
  Skills: 'Keep used, familiar and adjacent skills separate. Link evidence where available.',
  Projects: 'Only confirmed projects are available to resume and email generation.',
  Evidence:
    'Record facts with their source. Needs review and planned entries are excluded from resume and email generation. Confirmed means you confirmed it.',
}
const display = (value: Value) => (Array.isArray(value) ? value.join('\n') : value === undefined ? '' : String(value))
const title = (row: Row, index: number) =>
  String(
    row.name ||
      row.company ||
      row.institution ||
      row.text ||
      row.label ||
      row.market ||
      row.country ||
      `New entry ${index + 1}`,
  )

function Fields({
  fields,
  row,
  onChange,
  evidence,
}: {
  fields: Field[]
  row: Row
  onChange: (key: string, value: Value) => void
  evidence: Row[]
}) {
  return (
    <div className="profile-fields">
      {fields.map((field) =>
        field.kind === 'evidence' ? (
          <fieldset key={field.key} className="profile-evidence profile-wide">
            <legend>{field.label}</legend>
            {!evidence.length && <small>Add facts in Evidence first.</small>}
            <div>
              {evidence.map((item) => (
                <label key={item.id}>
                  <input
                    type="checkbox"
                    checked={((row[field.key] ?? []) as string[]).includes(item.id!)}
                    onChange={(event) => {
                      const current = (row[field.key] ?? []) as string[]
                      onChange(
                        field.key,
                        event.target.checked ? [...current, item.id!] : current.filter((id) => id !== item.id),
                      )
                    }}
                  />
                  <span>
                    {String(item.text)} <small>({String(item.status).replace('_', ' ')})</small>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
        ) : (
          <label
            key={field.key}
            className={['textarea', 'lines', 'evidence'].includes(field.kind ?? '') ? 'profile-wide' : ''}
          >
            <span>{field.label}</span>
            {field.options || field.kind === 'boolean' ? (
              <select
                value={display(row[field.key])}
                onChange={(event) => onChange(field.key, parseValue(field, event.target.value))}
              >
                {field.kind === 'boolean' ? (
                  <>
                    <option value="">Unknown / use default</option>
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </>
                ) : (
                  <>
                    <option value="">Choose…</option>
                    {field.options!.map((option) => (
                      <option key={option} value={option}>
                        {option.replace('_', ' ')}
                      </option>
                    ))}
                  </>
                )}
              </select>
            ) : field.kind === 'textarea' || field.kind === 'lines' ? (
              <textarea
                rows={field.kind === 'textarea' ? 4 : 3}
                value={display(row[field.key])}
                onChange={(event) => onChange(field.key, parseValue(field, event.target.value))}
              />
            ) : (
              <input
                type={field.kind ?? 'text'}
                step={field.kind === 'number' ? '1' : undefined}
                value={display(row[field.key])}
                onChange={(event) => onChange(field.key, parseValue(field, event.target.value))}
              />
            )}
            {field.hint && <small>{field.hint}</small>}
          </label>
        ),
      )}
    </div>
  )
}

function Collection({
  name,
  rows,
  fields,
  onChange,
  evidence,
  canRemove,
}: {
  name: string
  rows: Row[]
  fields: Field[]
  onChange: (rows: Row[]) => void
  evidence: Row[]
  canRemove?: (row: Row) => string | undefined
}) {
  const [open, setOpen] = useState<number | null>(null)
  const [error, setError] = useState('')
  return (
    <div className="profile-collection">
      {!rows.length && <p className="profile-hint">No {name} added yet.</p>}
      {rows.map((row, index) => (
        <div className="profile-entry" key={row.id ?? index}>
          <button
            type="button"
            className="profile-entry-toggle"
            aria-expanded={open === index}
            onClick={() => setOpen(open === index ? null : index)}
          >
            <span>{title(row, index)}</span>
            <small>{row.status ? String(row.status).replace('_', ' ') : row.level ? String(row.level) : 'Edit'}</small>
          </button>
          {open === index && (
            <div className="profile-entry-body">
              <Fields
                fields={fields}
                row={row}
                evidence={evidence}
                onChange={(key, value) =>
                  onChange(rows.map((item, i) => (i === index ? { ...item, [key]: value } : item)))
                }
              />
              <button
                type="button"
                className="profile-remove"
                onClick={() => {
                  const reason = canRemove?.(row)
                  if (reason) {
                    setError(reason)
                    return
                  }
                  onChange(rows.filter((_, i) => i !== index))
                  setOpen(null)
                  setError('')
                }}
              >
                Remove entry
              </button>
            </div>
          )}
        </div>
      ))}
      {error && (
        <p role="alert" className="errors">
          {error}
        </p>
      )}
      <button
        type="button"
        onClick={() => {
          onChange([...rows, newRow(name)])
          setOpen(rows.length)
          setError('')
        }}
      >
        Add{' '}
        {name === 'workAuthorization'
          ? 'country'
          : name === 'links'
            ? 'link'
            : name === 'compensation'
              ? 'compensation'
              : 'entry'}
      </button>
    </div>
  )
}

export default function Profile() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [baseline, setBaseline] = useState('')
  const [section, setSection] = useState<Section>('Identity')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [message, setMessage] = useState('')
  const errorRef = useRef<HTMLDivElement>(null)
  const dirty = snapshot !== null && JSON.stringify(snapshot) !== baseline
  async function load() {
    setBusy(true)
    setError('')
    try {
      const loaded = (await profileRequest()) ?? emptySnapshot()
      setSnapshot(loaded)
      setBaseline(JSON.stringify(loaded))
      setConflict(false)
      setMessage('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load the profile.')
    } finally {
      setBusy(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])
  useEffect(() => {
    if (error) errorRef.current?.focus()
  }, [error])
  function changeProfile(key: string, value: unknown) {
    setSnapshot((current) => (current ? { ...current, profile: { ...current.profile, [key]: value } } : current))
    setMessage('')
  }
  async function save() {
    if (!snapshot) return
    setBusy(true)
    setError('')
    setMessage('')
    try {
      const saved = await profileRequest(snapshot)
      if (saved) {
        setSnapshot(saved)
        setBaseline(JSON.stringify(saved))
        setMessage(`Saved revision ${saved.revision}.`)
        setConflict(false)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save the profile.')
      setConflict(err instanceof ProfileRequestError && err.status === 409)
    } finally {
      setBusy(false)
    }
  }
  function download() {
    if (!snapshot) return
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(snapshot, null, 2)], {
        type: 'application/json',
      }),
    )
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'profile-edits.json'
    anchor.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  if (!snapshot)
    return (
      <main className="profile-page">
        <h2>Profile</h2>
        {error ? (
          <>
            <p role="alert" className="errors">
              {error}
            </p>
            <button onClick={() => void load()}>Retry loading</button>
          </>
        ) : (
          <p role="status">Loading profile…</p>
        )}
      </main>
    )
  const p = snapshot.profile
  const evidence = p.background.evidence ?? []
  const backgroundKey = section.toLowerCase() as BackgroundKey
  const preferences = p.search.preferences
  return (
    <main className="profile-page">
      <header className="profile-header">
        <div>
          <h2>Profile</h2>
          <p>Your facts, preferences and outreach defaults. Changes apply after you save.</p>
        </div>
        <div className="profile-revision">
          {snapshot.revision ? (
            <>
              Revision {snapshot.revision}
              <br />
              {snapshot.updatedAt && new Date(snapshot.updatedAt).toLocaleString()}
            </>
          ) : (
            'No profile saved yet'
          )}
        </div>
      </header>
      <div className="profile-layout">
        <nav className="profile-sections" aria-label="Profile sections">
          {sections.map((item) => (
            <button
              type="button"
              key={item}
              aria-current={section === item ? 'page' : undefined}
              onClick={() => setSection(item)}
            >
              {item}
              {item.toLowerCase() in p.background && (
                <span>{p.background[item.toLowerCase() as BackgroundKey]?.length ?? 0}</span>
              )}
            </button>
          ))}
        </nav>
        <form
          className="profile-editor"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            void save()
          }}
        >
          {error && (
            <div ref={errorRef} tabIndex={-1} role="alert" className="profile-error">
              <strong>Profile was not saved</strong>
              <p>{error}</p>
              {conflict && (
                <div className="profile-actions">
                  <button type="button" onClick={download}>
                    Download my edits
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      if (window.confirm('Discard your edits and load the latest saved profile?')) void load()
                    }}
                  >
                    Discard edits and load latest
                  </button>
                </div>
              )}
            </div>
          )}
          <fieldset disabled={busy} className="profile-section">
            <legend>{section}</legend>
            {hints[section] && <p className="profile-hint">{hints[section]}</p>}
            {section === 'Identity' ? (
              <>
                <Fields
                  fields={identityFields}
                  row={p.identity}
                  evidence={evidence}
                  onChange={(key, value) => changeProfile('identity', { ...p.identity, [key]: value })}
                />
                <h3>Links</h3>
                <Collection
                  name="links"
                  rows={p.identity.links ?? []}
                  fields={[
                    { key: 'label', label: 'Link label' },
                    { key: 'url', label: 'URL', kind: 'url' },
                  ]}
                  evidence={evidence}
                  onChange={(links) => changeProfile('identity', { ...p.identity, links })}
                />
              </>
            ) : section === 'Preferences' ? (
              <>
                <Fields
                  fields={preferenceFields}
                  row={preferences}
                  evidence={evidence}
                  onChange={(key, value) =>
                    changeProfile('search', {
                      ...p.search,
                      preferences: { ...preferences, [key]: value },
                    })
                  }
                />
                <Fields
                  fields={[
                    {
                      key: 'maxRequiredExperienceYears',
                      label: 'Maximum required experience (years)',
                      kind: 'number',
                    hint: 'Leave blank to apply no experience ceiling.',
                    },
                  ]}
                  row={p.search as Row}
                  evidence={evidence}
                  onChange={(key, value) => changeProfile('search', { ...p.search, [key]: value })}
                />
                <h3>Work authorization</h3>
                <Collection
                  name="workAuthorization"
                  rows={preferences.workAuthorization ?? []}
                  fields={[
                    {
                      key: 'country',
                      label: 'Country code',
                      hint: 'Two uppercase letters, e.g. IN.',
                    },
                    {
                      key: 'authorized',
                      label: 'Authorized to work',
                      kind: 'boolean',
                    },
                    {
                      key: 'requiresSponsorship',
                      label: 'Requires sponsorship',
                      kind: 'boolean',
                    },
                  ]}
                  evidence={evidence}
                  onChange={(workAuthorization) =>
                    changeProfile('search', {
                      ...p.search,
                      preferences: { ...preferences, workAuthorization },
                    })
                  }
                />
                <h3>Compensation</h3>
                <p className="profile-hint">Whole currency units (2500000 for ₹25 lakh). Blank amounts stay unknown.</p>
                <Collection
                  name="compensation"
                  rows={preferences.compensation ?? []}
                  fields={[
                    { key: 'market', label: 'Market' },
                    {
                      key: 'currency',
                      label: 'Currency code',
                      hint: 'Three uppercase letters, e.g. INR or USD.',
                    },
                    {
                      key: 'period',
                      label: 'Period',
                      options: ['annual', 'monthly', 'hourly'],
                    },
                    {
                      key: 'basis',
                      label: 'Basis',
                      options: ['fixed', 'total'],
                    },
                    { key: 'minimum', label: 'Minimum', kind: 'number' },
                    { key: 'target', label: 'Target', kind: 'number' },
                  ]}
                  evidence={evidence}
                  onChange={(compensation) =>
                    changeProfile('search', {
                      ...p.search,
                      preferences: { ...preferences, compensation },
                    })
                  }
                />
              </>
            ) : section === 'Outreach' ? (
              <Fields
                fields={outreachFields}
                row={p.outreach}
                evidence={evidence}
                onChange={(key, value) => changeProfile('outreach', { ...p.outreach, [key]: value })}
              />
            ) : (
              <Collection
                key={backgroundKey}
                name={backgroundKey}
                rows={p.background[backgroundKey] ?? []}
                fields={backgroundFields[backgroundKey]}
                evidence={evidence}
                onChange={(rows) =>
                  changeProfile('background', {
                    ...p.background,
                    [backgroundKey]: rows,
                  })
                }
                canRemove={
                  backgroundKey === 'evidence'
                    ? (row) => {
                        const users = evidenceUsers(p.background, row.id!)
                        return users.length
                          ? `This fact is linked to ${users.join(', ')}. Remove those links before deleting it.`
                          : undefined
                      }
                    : undefined
                }
              />
            )}
          </fieldset>
          <footer className="profile-save">
            <div role="status" aria-live="polite">
              {busy
                ? 'Saving / loading…'
                : message ||
                  (dirty
                    ? 'Unsaved changes'
                    : snapshot.revision
                      ? 'All changes saved'
                      : 'Add what you know. You can save an incomplete profile.')}
            </div>
            <button
              type="submit"
              className="profile-primary"
              disabled={busy || conflict || (!dirty && snapshot.revision > 0)}
            >
              {busy ? 'Please wait…' : 'Save profile'}
            </button>
          </footer>
          <p className="profile-footnote">
            Saving updates the shared profile. Existing resumes and drafts are not regenerated here. Source polling,
            automation and template settings are preserved; their controls are outside this editor.
          </p>
        </form>
      </div>
    </main>
  )
}
