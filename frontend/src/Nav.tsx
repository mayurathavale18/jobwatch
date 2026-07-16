export default function Nav({ active }: { active: 'jobs' | 'cron' }) {
  return (
    <nav className="tabs">
      <a href="/" className={active === 'jobs' ? 'active' : ''}>
        Jobs
      </a>
      <a href="/cron" className={active === 'cron' ? 'active' : ''}>
        Cron
      </a>
    </nav>
  )
}
