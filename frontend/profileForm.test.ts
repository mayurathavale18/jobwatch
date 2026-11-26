import test from 'node:test'
import assert from 'node:assert/strict'
import {
  emptySnapshot,
  newRow,
  parseValue,
  prepareSave,
  evidenceUsers,
  friendlyError,
  profileRequest,
  ProfileRequestError,
} from './src/profileForm.ts'

test('editing preserves hidden settings, IDs, links and unknown optional values', () => {
  const snapshot = emptySnapshot()
  snapshot.profile.search.sources = [{ provider: 'lever', slug: 'fictional' }]
  snapshot.profile.search.filters = { includeKeywords: [' engineer '] }
  snapshot.profile.automation = { dailyBudget: 20 }
  snapshot.profile.resume = { template: 'default', maxPages: 1 }
  snapshot.profile.background.evidence = [
    {
      id: 'fact',
      text: 'Fictional API work',
      source: 'Candidate',
      status: 'needs_review',
    },
  ]
  snapshot.profile.background.skills = [{ id: 'go', name: 'Go', level: 'used', evidenceIds: ['fact'] }]
  snapshot.profile.identity = {
    ...snapshot.profile.identity,
    name: 'Sam Fictional',
  }
  snapshot.profile.search.preferences.willingToRelocate = parseValue(
    { key: 'willingToRelocate', label: 'Relocate', kind: 'boolean' },
    '',
  )
  const saved = prepareSave(snapshot)
  assert.deepEqual(saved.profile.search.sources, snapshot.profile.search.sources)
  assert.deepEqual(saved.profile.search.filters, snapshot.profile.search.filters)
  assert.deepEqual(saved.profile.automation, { dailyBudget: 20 })
  assert.deepEqual(saved.profile.resume, { template: 'default', maxPages: 1 })
  assert.deepEqual(saved.profile.background, snapshot.profile.background)
  assert.equal('willingToRelocate' in saved.profile.search.preferences, false)
  assert.deepEqual(evidenceUsers(saved.profile.background, 'fact'), ['Go'])
})
test('optional numbers and booleans retain zero, false and unknown distinctly', () => {
  const number = { key: 'minimum', label: 'Minimum', kind: 'number' } as const
  const boolean = {
    key: 'authorized',
    label: 'Authorized',
    kind: 'boolean',
  } as const
  assert.equal(parseValue(number, ''), undefined)
  assert.equal(parseValue(number, '0'), 0)
  assert.equal(parseValue(boolean, ''), undefined)
  assert.equal(parseValue(boolean, 'false'), false)
  assert.equal(parseValue(boolean, 'true'), true)
})
test('list editing keeps newlines while typing; saving removes empty lines', () => {
  const lines = {
    key: 'workModes',
    label: 'Work modes',
    kind: 'lines',
  } as const
  const snapshot = emptySnapshot()
  snapshot.profile.search.preferences.workModes = parseValue(lines, 'remote\n\n hybrid\n')
  assert.deepEqual(snapshot.profile.search.preferences.workModes, ['remote', '', ' hybrid', ''])
  assert.deepEqual(prepareSave(snapshot).profile.search.preferences.workModes, ['remote', 'hybrid'])
})
test('new claims start unconfirmed and generated IDs remain unique', () => {
  assert.equal(newRow('projects').status, 'needs_review')
  assert.equal(newRow('evidence').status, 'needs_review')
  assert.notEqual(newRow('skills').id, newRow('skills').id)
})
test('unsafe whole numbers fail instead of silently overwriting rounded values', async () => {
  const snapshot = emptySnapshot()
  snapshot.profile.outreach.maxEmployees = Number.MAX_SAFE_INTEGER + 1
  assert.throws(() => prepareSave(snapshot), /whole-number range/)
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response('{"revision":9007199254740993}')
    await assert.rejects(profileRequest(), /whole-number range/)
  } finally {
    globalThis.fetch = original
  }
})
test('validation errors identify entries without hiding the server message', () => {
  assert.equal(friendlyError('Load the latest profile.'), 'Load the latest profile.')
  assert.equal(
    friendlyError('profile.background.skills[2].evidenceIds: missing'),
    'skills entry 3.supporting evidence: missing',
  )
})
test('requests distinguish absent profiles from errors and propagate conflicts', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response('No profile', { status: 404 })
    assert.equal(await profileRequest(), null)
    await assert.rejects(
      profileRequest(emptySnapshot()),
      (err: unknown) => err instanceof ProfileRequestError && err.status === 404,
    )
    globalThis.fetch = async () => new Response('Changed elsewhere', { status: 409 })
    await assert.rejects(
      profileRequest(emptySnapshot()),
      (err: unknown) => err instanceof ProfileRequestError && err.status === 409 && err.message === 'Changed elsewhere',
    )
  } finally {
    globalThis.fetch = original
  }
})
