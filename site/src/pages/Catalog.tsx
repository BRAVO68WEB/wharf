import { useEffect, useState } from 'react'
import { Link, useLoaderData, useRevalidator, useSearchParams, type LoaderFunctionArgs } from 'react-router-dom'
import { api, guard, loadSession, type Repo, type RepoList } from '../api'
import { formatBytes, formatWhen, pageCount, pullCommand, repoPath, shortDigest } from '../format'
import { CopyButton } from '../ui'

export type CatalogData = RepoList & { signedIn: boolean }

export async function catalogLoader({ request }: LoaderFunctionArgs): Promise<CatalogData> {
  const session = await loadSession()
  const url = new URL(request.url)
  const search = url.searchParams.get('search') ?? ''
  const page = Math.max(1, Number(url.searchParams.get('page') || '1') || 1)
  const query = new URLSearchParams()
  if (search) query.set('search', search)
  if (page > 1) query.set('page', String(page))
  const suffix = query.toString()
  const path = (session ? '/dashboard/api/repos' : '/dashboard/api/public/repos') + (suffix ? `?${suffix}` : '')
  const list = session ? await guard(api<RepoList>(path)) : await api<RepoList>(path)
  return { ...list, signedIn: session !== null }
}

export function Catalog() {
  const data = useLoaderData() as CatalogData
  const [params, setParams] = useSearchParams()
  const search = params.get('search') ?? ''
  const [text, setText] = useState(search)

  useEffect(() => {
    setText(search)
  }, [search])

  useEffect(() => {
    const handle = window.setTimeout(() => {
      setParams(
        (prev) => {
          const current = prev.get('search') ?? ''
          if (current === text) return prev
          const next = new URLSearchParams(prev)
          if (text) next.set('search', text)
          else next.delete('search')
          next.delete('page')
          return next
        },
        { replace: true },
      )
    }, 180)
    return () => window.clearTimeout(handle)
  }, [text, setParams])

  const needle = data.search.toLowerCase()
  const rows: Repo[] = []
  if (data.mounted && (!needle || data.mounted.name.toLowerCase().includes(needle))) {
    rows.push(data.mounted)
  }
  rows.push(...data.repos)
  const pages = pageCount(data.total, data.pageSize)

  return (
    <>
      <h1>Repositories</h1>
      <p className="lede">
        {data.signedIn
          ? 'Images stored in this registry. Publish one to list it here without signing in, and to allow docker pull.'
          : 'Published images. docker pull works for these without signing in.'}
      </p>
      <div className="toolbar">
        <form role="search" onSubmit={(event) => event.preventDefault()}>
          <label className="search">
            <span className="visually-hidden">Find a repository</span>
            <input
              value={text}
              onChange={(event) => setText(event.target.value)}
              placeholder="Search repositories"
            />
          </label>
        </form>
      </div>
      <div className="panel">
        {rows.length === 0 ? (
          <p className="empty">
            {data.search
              ? 'No repositories match that search.'
              : data.signedIn
                ? 'No repositories yet. Push an image and it shows up here.'
                : 'Nothing is published.'}
          </p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Last updated</th>
                <th>Tags</th>
                <th>Size</th>
                <th>Digest</th>
                {data.signedIn ? <th>Publish</th> : null}
              </tr>
            </thead>
            <tbody>
              {rows.map((repo) => (
                <tr key={repo.name}>
                  <td data-label="Name">
                    {data.signedIn ? (
                      <Link className="name-link" to={repoPath(repo.name)}>
                        {repo.name}
                      </Link>
                    ) : (
                      <span>{repo.name}</span>
                    )}
                    {repo.pullHost && repo.latestTag ? (
                      <span className="pull-line">
                        <span>{pullCommand(repo.pullHost, repo.name, repo.latestTag)}</span>
                        <CopyButton value={pullCommand(repo.pullHost, repo.name, repo.latestTag)} label="Copy" />
                      </span>
                    ) : null}
                  </td>
                  <td data-label="Last updated">
                    <time dateTime={repo.updatedAt}>{formatWhen(repo.updatedAt)}</time>
                  </td>
                  <td data-label="Tags">{repo.tagCount}</td>
                  <td data-label="Size">{formatBytes(repo.sizeBytes)}</td>
                  <td data-label="Digest">
                    {repo.digest ? (
                      <span className="digest-cell">
                        <span className="mono" title={repo.digest}>
                          {shortDigest(repo.digest)}
                        </span>
                        <CopyButton value={repo.digest} label="Copy" />
                      </span>
                    ) : (
                      <span className="lede">No tag</span>
                    )}
                  </td>
                  {data.signedIn ? (
                    <td data-label="Publish">
                      <PublishControl repo={repo} />
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {pages > 1 ? (
        <nav className="pager" aria-label="Repository pages">
          {data.page > 1 ? <Link to={pageHref(data.search, data.page - 1)}>Previous</Link> : <span>Previous</span>}
          <span>
            Page {data.page} of {pages}
          </span>
          {data.page < pages ? <Link to={pageHref(data.search, data.page + 1)}>Next</Link> : <span>Next</span>}
        </nav>
      ) : null}
    </>
  )
}

function PublishControl({ repo }: { repo: Repo }) {
  const revalidator = useRevalidator()
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function setPublished(published: boolean) {
    setBusy(true)
    setError('')
    try {
      await api('/dashboard/api/repos/publish', {
        method: 'POST',
        body: JSON.stringify({ name: repo.name, published }),
      })
      setConfirming(false)
      revalidator.revalidate()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not update publishing.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      {repo.published && !confirming ? (
        <button className="btn-ghost" type="button" disabled={busy} onClick={() => void setPublished(false)}>
          Unpublish
        </button>
      ) : confirming ? (
        <div className="row-actions">
          <button className="btn" type="button" disabled={busy} onClick={() => void setPublished(true)}>
            Publish
          </button>
          <button className="btn-ghost" type="button" disabled={busy} onClick={() => setConfirming(false)}>
            Cancel
          </button>
        </div>
      ) : (
        <button className="btn-ghost" type="button" disabled={busy} onClick={() => setConfirming(true)}>
          Publish
        </button>
      )}
      {error ? (
        <p className="alert" role="alert">
          {error}
        </p>
      ) : null}
    </>
  )
}

function pageHref(search: string, page: number): string {
  const query = new URLSearchParams()
  if (search) query.set('search', search)
  if (page > 1) query.set('page', String(page))
  const suffix = query.toString()
  return suffix ? `/?${suffix}` : '/'
}
