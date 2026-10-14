import assert from 'node:assert/strict'
import { captureInstahyreApplication } from './instahyre.mjs'

const fields = {
  '.profile-info h2': 'Example', '.profile-info h1': 'Engineer', '.profile-info .location-span': 'Bangalore',
  '#job-description': 'Python APIs. Contact hiring@example.test', '.apply.applied': 'Application Sent!',
  '.apply.applied span.ng-binding': 'on October 4, 2026', '.right-section-modal .rec-name': 'Example Recruiter',
  '.right-section-modal .designation': 'Recruiter@Agency.test at Agency',
}
const doc = {location: {href: 'https://www.instahyre.com/candidate/opportunities/?status=1'}, querySelector: selector => ({innerText: fields[selector] || ''})}
const captured = captureInstahyreApplication(doc)
assert.equal(captured.contacts.length, 2)
assert.equal(captured.contacts[0].email, 'Recruiter@Agency.test')
assert.match(captured.contacts[0].evidence, /Agency/)
assert.equal(captured.contacts[1].email, 'hiring@example.test')
assert.equal(captured.appliedAt, 'October 4, 2026')
fields['#job-description'] = 'Python  APIs.   Contact hiring@example.test'
assert.equal(captureInstahyreApplication(doc).sourceId, captured.sourceId)
fields['#job-description'] += ' A distinct role.'
assert.notEqual(captureInstahyreApplication(doc).sourceId, captured.sourceId)
fields['.right-section-modal .designation'] = 'Talent Acquisition at Agency'
assert.equal(captureInstahyreApplication(doc).contacts[0].email, '')
fields['.apply.applied'] = 'Apply now'
assert.throws(() => captureInstahyreApplication(doc), /applied role/)
console.log('Instahyre browser capture checks passed')
