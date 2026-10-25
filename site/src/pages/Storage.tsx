import { useState } from 'react'
import { useLoaderData, useRevalidator } from 'react-router-dom'
import { api, guard, requireSession, type GCResult, type GCStats } from '../api'
import { formatBytes, formatWhen, shortDigest } from '../format'
import { CopyButton } from '../ui'

export async function storageLoader(): Promise<GCStats> {
  await requireSession()
  return guard(api<GCStats>('/dashboard/api/gc'))
}

function formatInterval(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400}d`
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

export function Storage() {
  const initial = useLoaderData() as GCStats
  const revalidator = useRevalidator()
  const [result, setResult] = useState<GCResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const stats = result?.stats ?? initial

  async function dryRun() {
    setBusy(true)
    setError('')
    setConfirming(false)
    try {
      setResult(await api<GCResult>('/dashboard/api/gc/dry-run', { method: 'POST' }))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Dry run failed.')
    } finally {
      setBusy(false)
    }
  }

  async function run() {
    setBusy(true)
    setError('')
    try {
      setResult(await api<GCResult>('/dashboard/api/gc/run', { method: 'POST' }))
      setConfirming(false)
      revalidator.revalidate()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Collection failed.')
    } finally {
      setBusy(false)
    }
  }

  const clear = stats.unreferencedBlobs === 0

  return (
    <article>
      <h1>Storage</h1>
      <p className="lede">
        {clear
          ? 'Nothing is waiting to be collected. Unreferenced blobs stay on disk until you delete them.'
          : 'A dry run lists unreferenced blobs before anything is deleted.'}{' '}
        {stats.intervalSeconds > 0
          ? `A collection also runs every ${formatInterval(stats.intervalSeconds)}.`
          : 'Scheduled collection is off.'}
        {stats.lastRunAt ? ` Last run ${formatWhen(stats.lastRunAt)}.` : ''}
      </p>
      <section className="panel section">
        <div className="stats">
          <p className="stat">
            <span>Total blobs</span>
            <strong>{stats.totalBlobs}</strong>
          </p>
          <p className="stat">
            <span>Referenced</span>
            <strong>{stats.referencedBlobs}</strong>
          </p>
          <p className="stat">
            <span>Unreferenced</span>
            <strong>{stats.unreferencedBlobs}</strong>
          </p>
          <p className="stat">
            <span>Reclaimable</span>
            <strong>{formatBytes(stats.unreferencedBytes)}</strong>
          </p>
        </div>
      </section>
      <div className="actions">
        <button className="btn-ghost" type="button" onClick={() => void dryRun()} disabled={busy}>
          Dry run
        </button>
        {confirming ? (
          <p className="confirm">
            This will delete {stats.unreferencedBlobs} blobs, total {formatBytes(stats.unreferencedBytes)}.{' '}
            <button className="btn-danger" type="button" onClick={() => void run()} disabled={busy}>
              Delete unreferenced blobs
            </button>{' '}
            <button className="btn-ghost" type="button" onClick={() => setConfirming(false)} disabled={busy}>
              Cancel
            </button>
          </p>
        ) : (
          <button className="btn-danger" type="button" onClick={() => setConfirming(true)} disabled={busy || clear}>
            Delete unreferenced blobs
          </button>
        )}
      </div>
      {error ? (
        <p className="alert" role="alert">
          {error}
        </p>
      ) : null}
      {result ? (
        <section className="panel section">
          <div className="section-head">
            <h2>{result.ran ? 'Deleted' : 'Would delete'}</h2>
            <span>
              {result.blobs.length} blobs, {formatBytes(result.bytes)}
              {result.failed > 0 ? `, ${result.failed} failed` : ''}
            </span>
          </div>
          {result.blobs.length === 0 ? (
            <p className="empty">Nothing to delete.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Digest</th>
                  <th>Size</th>
                </tr>
              </thead>
              <tbody>
                {result.blobs.map((blob) => (
                  <tr key={blob.digest}>
                    <td data-label="Digest">
                      <span className="digest-cell">
                        <span className="mono" title={blob.digest}>
                          {shortDigest(blob.digest)}
                        </span>
                        <CopyButton value={blob.digest} label="Copy" />
                      </span>
                    </td>
                    <td data-label="Size">{formatBytes(blob.size)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      ) : null}
    </article>
  )
}
