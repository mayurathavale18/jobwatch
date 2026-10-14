// Pass this function to the built-in browser's read-only DOM evaluator after
// opening an Applied jobs card and waiting for #job-description to render.
export function captureInstahyreApplication(doc = document) {
  const text = selector => (doc.querySelector(selector)?.innerText || '').trim()
  const company = text('.profile-info h2')
  const title = text('.profile-info h1')
  const location = text('.profile-info .location-span')
  const jd = text('#job-description')
  const appliedAt = text('.apply.applied span.ng-binding').replace(/^on\s+/, '')
  if (!company || !title || !jd || !text('.apply.applied').includes('Application Sent')) {
    throw new Error('Open an applied role and wait for its complete JD before capture')
  }
  const emails = value => [...new Set(value.match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi) || [])]
  const name = text('.right-section-modal .rec-name')
  const role = text('.right-section-modal .designation')
  const recruiterEmails = emails(`${name}\n${role}`)
  const contacts = recruiterEmails.map(email => ({name, email, role, evidence: `Displayed recruiter profile: ${name} — ${role}`}))
  if (name && !recruiterEmails.length) contacts.push({name, email: '', role, evidence: 'Recruiter displayed on the application'})
  for (const email of emails(jd)) {
    if (!contacts.some(c => c.email.toLowerCase() === email.toLowerCase())) {
      contacts.push({name: '', email, role: 'Email listed in JD', evidence: jd.split('\n').find(line => line.includes(email)) || email})
    }
  }
  // shortcut: the rendered UI has no platform ID; a changed JD requires identity review on the next capture.
  let hash = 14695981039346656037n
  for (const char of JSON.stringify([company, title, location, appliedAt, jd.replace(/\s+/g, ' ').trim()])) {
    hash = BigInt.asUintN(64, (hash ^ BigInt(char.codePointAt(0))) * 1099511628211n)
  }
  return {
    platform: 'instahyre', sourceId: `snapshot-v1:${hash.toString(16).padStart(16, '0')}`,
    company, title, location, url: doc.location.href, appliedAt, sourceStatus: 'Application Sent', jd, contacts,
    metadata: {
      experience: text('.profile-info .experience'), companyOverview: text('#employer-summary'),
      skills: text('#job-skills-description'), companyAtAGlance: text('.left-section-modal'), capturedAt: new Date().toISOString(),
      identityMethod: 'Browser snapshot fingerprint: company, role, location, application date and normalized JD. Re-capture after a JD edit needs identity review; no platform ID is exposed.',
    },
  }
}
