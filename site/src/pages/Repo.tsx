import { useState } from 'react'
import { Link, useNavigate, useRevalidator, type LoaderFunctionArgs } from 'react-router-dom'
import {
  api,
  guard,
  repoAPI,
  requireSession,
  type ImageConfig,
  type Manifest,
  type Tag,
  type TagList,
} from '../api'
import { decodeSplat, displayCommand, formatBytes, formatWhen, manifestPath, pullCommand, repoPath, shortDigest } from '../format'
import { Chevron, CopyButton } from '../ui'

export type RepoData =
  | { kind: 'tags'; tags: TagList }
  | { kind: 'manifest'; manifest: Manifest }

export async function repoLoader({ params }: LoaderFunctionArgs): Promise<RepoData> {
  await requireSession()
  const { name, digest } = decodeSplat(params['*'] ?? '')
  if (!name) throw new Response('not found', { status: 404 })
  if (digest) {
    const manifest = await guard(api<Manifest>(`${repoAPI(name)}/manifests/${encodeURIComponent(digest)}`))
    return { kind: 'manifest', manifest }
  }
  const tags = await guard(api<TagList>(repoAPI(name)))
  return { kind: 'tags', tags }
}

export function RepoView({ data }: { data: RepoData }) {
  if (data.kind === 'manifest') return <ManifestView manifest={data.manifest} />
  return <TagsView list={data.tags} />
}

function TagsView({ list }: { list: TagList }) {
  return (
    <article>
      <p className="crumbs">
        <Link to="/">Repositories</Link>
        <span>/</span>
        <span>{list.name}</span>
      </p>
      <h1>{list.name}</h1>
      <div className="tabs" role="tablist" aria-label="Repository">
        <span aria-current="page">Tags</span>
      </div>
      {list.tags.length === 0 ? (
        <div className="panel">
          <p className="empty">This repository has no tags.</p>
        </div>
      ) : (
        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>Tag</th>
                <th>Last pushed</th>
                <th>Size</th>
                <th>Digest</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {list.tags.map((tag) => (
                <TagRow key={tag.name} repo={list.name} tag={tag} pullHost={list.pullHost} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </article>
  )
}

function TagRow({ repo, tag, pullHost }: { repo: string; tag: Tag; pullHost?: string }) {
  const revalidator = useRevalidator()
  const navigate = useNavigate()
  const [mode, setMode] = useState<'idle' | 'delete' | 'retag'>('idle')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [targetRepo, setTargetRepo] = useState(repo)
  const [targetTag, setTargetTag] = useState('')

  async function remove() {
    setBusy(true)
    setError('')
    try {
      await api<void>(`${repoAPI(repo)}/tags/${encodeURIComponent(tag.name)}`, { method: 'DELETE' })
      setMode('idle')
      revalidator.revalidate()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed.')
    } finally {
      setBusy(false)
    }
  }

  async function copyTag() {
    setBusy(true)
    setError('')
    try {
      const copied = await api<{ repo: string }>(`${repoAPI(repo)}/tags/${encodeURIComponent(tag.name)}/retag`, {
        method: 'POST',
        body: JSON.stringify({ targetRepo, targetTag }),
      })
      setMode('idle')
      if (copied.repo !== repo) {
        navigate(repoPath(copied.repo))
        return
      }
      revalidator.revalidate()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Retag failed.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <tr>
      <td data-label="Tag">
        <Link className="name-link" to={`${manifestPath(repo, tag.digest)}?tag=${encodeURIComponent(tag.name)}`}>
          {tag.name}
        </Link>
        {pullHost ? (
          <span className="pull-line">
            <span>{pullCommand(pullHost, repo, tag.name)}</span>
            <CopyButton value={pullCommand(pullHost, repo, tag.name)} label="Copy" />
          </span>
        ) : null}
        {mode === 'delete' ? (
          <p className="confirm">
            Unmap {tag.name}? The manifest stays until nothing points at it.{' '}
            <button className="btn-danger" type="button" onClick={() => void remove()} disabled={busy}>
              Delete tag
            </button>{' '}
            <button className="btn-ghost" type="button" onClick={() => setMode('idle')} disabled={busy}>
              Cancel
            </button>
          </p>
        ) : null}
        {mode === 'retag' ? (
          <form
            className="inline-form"
            onSubmit={(event) => {
              event.preventDefault()
              void copyTag()
            }}
          >
            <label>
              Repository
              <input value={targetRepo} onChange={(event) => setTargetRepo(event.target.value)} required />
            </label>
            <label>
              New tag
              <input value={targetTag} onChange={(event) => setTargetTag(event.target.value)} required />
            </label>
            <div className="actions">
              <button className="btn" type="submit" disabled={busy}>
                Copy tag
              </button>
              <button className="btn-ghost" type="button" onClick={() => setMode('idle')} disabled={busy}>
                Cancel
              </button>
            </div>
          </form>
        ) : null}
        {error ? (
          <p className="alert" role="alert">
            {error}
          </p>
        ) : null}
      </td>
      <td data-label="Last pushed">
        <time dateTime={tag.updatedAt}>{formatWhen(tag.updatedAt)}</time>
      </td>
      <td data-label="Size">{formatBytes(tag.size)}</td>
      <td data-label="Digest">
        <span className="digest-cell">
          <span className="mono" title={tag.digest}>
            {shortDigest(tag.digest)}
          </span>
          <CopyButton value={tag.digest} label="Copy" />
        </span>
      </td>
      <td data-label="Actions">
        <div className="row-actions">
          <button className="btn-ghost" type="button" onClick={() => setMode('retag')}>
            Retag
          </button>
          <button className="btn-danger" type="button" onClick={() => setMode('delete')}>
            Delete
          </button>
        </div>
      </td>
    </tr>
  )
}

function ManifestView({ manifest }: { manifest: Manifest }) {
  const navigate = useNavigate()
  const tag = new URLSearchParams(window.location.search).get('tag')
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function remove() {
    setBusy(true)
    setError('')
    try {
      await api<void>(`${repoAPI(manifest.repo)}/manifests/${encodeURIComponent(manifest.digest)}`, {
        method: 'DELETE',
      })
      navigate(repoPath(manifest.repo))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed.')
      setBusy(false)
    }
  }

  const imageBytes = manifest.layers.reduce((sum, layer) => sum + Math.max(0, layer.size), 0) || manifest.size

  return (
    <article>
      <p className="crumbs">
        <Link to="/">Repositories</Link>
        <span>/</span>
        <Link to={repoPath(manifest.repo)}>{manifest.repo}</Link>
        <span>/</span>
        <span>{tag ?? 'Image layers'}</span>
      </p>
      <h1>{tag ?? 'Image layers'}</h1>
      <p className="meta">
        <span>{formatBytes(imageBytes)}</span>
        <span>{manifest.mediaType}</span>
      </p>
      <div className="pull">
        <span className="mono">{manifest.digest}</span>
        <CopyButton value={manifest.digest} label="Copy digest" />
      </div>

      <section className="panel section">
        <div className="section-head">
          <h2>Image layers</h2>
        </div>
        {manifest.imageLayers.length === 0 ? (
          <p className="empty">This manifest has no image history.</p>
        ) : (
          <ol className="layers">
            {manifest.imageLayers.map((layer, index) => (
              <LayerRow key={`${index}-${layer.digest ?? 'empty'}`} index={index + 1} command={layer.command} size={layer.size} digest={layer.digest} />
            ))}
          </ol>
        )}
      </section>

      {manifest.config ? <ConfigFacts config={manifest.config} /> : null}

      <section className="panel section">
        <div className="section-head">
          <h2>Layer digests</h2>
        </div>
        {manifest.layers.length === 0 ? (
          <p className="empty">No descriptors.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Digest</th>
                <th>Size</th>
                <th>Media type</th>
              </tr>
            </thead>
            <tbody>
              {manifest.layers.map((layer) => (
                <tr key={layer.digest}>
                  <td data-label="Digest">
                    <span className="digest-cell">
                      <span className="mono" title={layer.digest}>
                        {shortDigest(layer.digest)}
                      </span>
                      <CopyButton value={layer.digest} label="Copy" />
                    </span>
                  </td>
                  <td data-label="Size">{formatBytes(layer.size)}</td>
                  <td data-label="Media type">{layer.mediaType}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <div className="actions">
        {confirming ? (
          <p className="confirm">
            Delete this manifest and every tag that points at it? The blobs stay until you collect them.{' '}
            <button className="btn-danger" type="button" onClick={() => void remove()} disabled={busy}>
              Delete manifest
            </button>{' '}
            <button className="btn-ghost" type="button" onClick={() => setConfirming(false)} disabled={busy}>
              Cancel
            </button>
          </p>
        ) : (
          <button className="btn-danger" type="button" onClick={() => setConfirming(true)}>
            Delete manifest
          </button>
        )}
      </div>
      {error ? (
        <p className="alert" role="alert">
          {error}
        </p>
      ) : null}
    </article>
  )
}

function LayerRow({
  index,
  command,
  size,
  digest,
}: {
  index: number
  command: string
  size: number
  digest?: string
}) {
  const [open, setOpen] = useState(false)
  const shown = displayCommand(command)
  return (
    <li className="layer">
      <button type="button" className="layer-summary" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
        <Chevron open={open} />
        <span className="layer-preview">
          <span className="layer-index">{index} </span>
          {shown}
        </span>
        <span className="layer-size">{formatBytes(size)}</span>
      </button>
      {open ? (
        <div className="layer-body">
          <pre>{shown}</pre>
          <div className="layer-foot">
            {digest ? <span className="mono">{digest}</span> : <span>This step did not add a layer.</span>}
            <CopyButton value={command} label="Copy command" />
          </div>
        </div>
      ) : null}
    </li>
  )
}

function ConfigFacts({ config }: { config: ImageConfig }) {
  const rows: Array<[string, string]> = []
  if (config.user) rows.push(['User', config.user])
  if (config.workingDir) rows.push(['Workdir', config.workingDir])
  if (config.entrypoint?.length) rows.push(['Entrypoint', config.entrypoint.join(' ')])
  if (config.cmd?.length) rows.push(['Cmd', config.cmd.join(' ')])
  if (config.exposedPorts?.length) rows.push(['Ports', config.exposedPorts.join(', ')])
  if (config.env?.length) rows.push(['Env', config.env.join('\n')])
  if (rows.length === 0) return null
  return (
    <section>
      <h2 className="section-title">Container</h2>
      <dl className="facts">
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
