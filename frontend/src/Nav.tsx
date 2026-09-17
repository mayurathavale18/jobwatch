export default function Nav({ active }: { active: 'jobs' | 'cron' | 'connections' }) {
  return (
    <nav className="tabs">
      <a href="/" className={active === 'jobs' ? 'active' : ''}>
        Jobs
      </a>
      <a href="/connections" className={active === 'connections' ? 'active' : ''}>
        Connections
      </a>
      <a href="/cron" className={active === 'cron' ? 'active' : ''}>
        Cron
      </a>
    </nav>
  )
}
