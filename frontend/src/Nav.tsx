export default function Nav({ active }: { active: 'jobs' | 'cron' | 'connections' | 'applications' }) {
  return (
    <nav className="tabs">
      <a href="/" className={active === 'jobs' ? 'active' : ''}>
        Jobs
      </a>
      <a href="/connections" className={active === 'connections' ? 'active' : ''}>
        Connections
      </a>
      <a href="/applications" className={active === 'applications' ? 'active' : ''}>
        Applications
      </a>
      <a href="/cron" className={active === 'cron' ? 'active' : ''}>
        Cron
      </a>
    </nav>
  )
}
