import Nav from './Nav'
import Jobs from './pages/Jobs'
import Cron from './pages/Cron'

function App() {
  const path = window.location.pathname
  const active = path === '/cron' ? 'cron' : 'jobs'

  return (
    <>
      <h1>jobwatch</h1>
      <Nav active={active} />
      {active === 'cron' ? <Cron /> : <Jobs />}
    </>
  )
}

export default App
