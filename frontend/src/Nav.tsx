export default function Nav({ active }: { active: 'jobs' | 'cron' | 'connections' | 'profile' }) {
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
      <a href="/profile" className={active === 'profile' ? 'active' : ''}>
        Profile
      </a>
    </nav>
  )
}
