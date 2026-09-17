import Nav from './Nav'
import Jobs from './pages/Jobs'
import Cron from './pages/Cron'
import Email from './pages/Email'
import Connections from './pages/Connections'

function App() {
  const path = window.location.pathname
  const emailMatch = path.match(/^\/jobs\/(\d+)\/email$/)
  const active = path === '/cron' ? 'cron' : path === '/connections' ? 'connections' : 'jobs'

  return (
    <>
      <h1>jobwatch</h1>
      <Nav active={active} />
      {emailMatch ? (
        <Email jobId={Number(emailMatch[1])} />
      ) : active === 'cron' ? (
        <Cron />
      ) : active === 'connections' ? (
        <Connections />
      ) : (
        <Jobs />
      )}
    </>
  )
}

export default App
