import { useEffect, useState } from 'react'
import { fetchConnectionsCount, uploadConnections } from '../api'

export default function Connections() {
  const [count, setCount] = useState<number | null>(null)
  const [msg, setMsg] = useState<string | null>(null)

  useEffect(() => {
    fetchConnectionsCount().then(setCount).catch((e) => setMsg(String(e)))
  }, [])

  async function handleFile(e: { target: { files: FileList | null } }) {
    const file = e.target.files?.[0]
    if (!file) return
    setMsg('Importing…')
    try {
      const n = await uploadConnections(await file.text())
      setCount(n)
      setMsg(`Imported ${n} connections (replaced the previous import).`)
    } catch (err) {
      setMsg(`Import failed: ${err instanceof Error ? err.message : String(err)}`)
    }
  }

  return (
    <div className="connections-page">
      <p>
        Imported LinkedIn connections: <span className="count">{count ?? '…'}</span>
      </p>
      <p className="muted">
        LinkedIn → Settings → Data privacy → Get a copy of your data → Connections. Unzip and upload
        Connections.csv here. Every job's Email page lists connections at that company as referral routes.
        Re-uploading replaces the old import.
      </p>
      <input type="file" accept=".csv" onChange={handleFile} />
      {msg && <p>{msg}</p>}
    </div>
  )
}
