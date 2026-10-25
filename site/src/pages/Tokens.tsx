import { useState, type FormEvent } from 'react'
import { useLoaderData, useRevalidator } from 'react-router-dom'
import { api, guard, requireSession } from '../api'
import { formatWhen } from '../format'
import { Modal } from '../modal'
import { CopyButton } from '../ui'

type TokenRow = {
  id: number
  name: string
  createdAt: string
}

type TokenList = { tokens: TokenRow[] }

export async function tokensLoader(): Promise<TokenList> {
  await requireSession()
  return guard(api<TokenList>('/dashboard/api/tokens'))
}

export function Tokens() {
  const data = useLoaderData() as TokenList
  const revalidator = useRevalidator()
  const [name, setName] = useState('')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  function close() {
    setOpen(false)
    setFormError('')
    setName('')
  }

  async function create(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setFormError('')
    setToken('')
    try {
      const created = await api<{ token: string }>('/dashboard/api/tokens', {
        method: 'POST',
        body: JSON.stringify({ name }),
      })
      setToken(created.token)
      close()
      revalidator.revalidate()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Could not create the token.')
    } finally {
      setBusy(false)
    }
  }

  async function revoke(row: TokenRow) {
    setError('')
    try {
      await api<void>(`/dashboard/api/tokens/${row.id}`, { method: 'DELETE' })
      revalidator.revalidate()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not revoke the token.')
    }
  }

  return (
    <article>
      <h1>Tokens</h1>
      <p className="lede">
        A token is a password for docker login. Your dashboard password keeps working after you revoke one. The token is shown once.
      </p>
      {token ? (
        <p className="pull">
          <span>{token}</span>
          <CopyButton value={token} label="Copy token" />
        </p>
      ) : null}
      {error ? (
        <p className="alert" role="alert">
          {error}
        </p>
      ) : null}
      <div className="actions">
        <button className="btn" type="button" onClick={() => setOpen(true)}>
          Create token
        </button>
      </div>
      <Modal open={open} title="Create token" titleId="create-token-title" onClose={close}>
        <form className="modal-form" onSubmit={(event) => void create(event)}>
          <div className="field">
            <label htmlFor="token-name">Name</label>
            <input
              id="token-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
              maxLength={64}
              autoComplete="off"
            />
          </div>
          {formError ? (
            <p className="alert" role="alert">
              {formError}
            </p>
          ) : null}
          <button className="btn login-submit" type="submit" disabled={busy}>
            Create token
          </button>
        </form>
      </Modal>
      <div className="panel section">
        {data.tokens.length === 0 ? (
          <p className="empty">No tokens yet.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {data.tokens.map((row) => (
                <tr key={row.id}>
                  <td data-label="Name">{row.name}</td>
                  <td data-label="Created">{formatWhen(row.createdAt)}</td>
                  <td data-label="Actions">
                    <div className="row-actions">
                      <button className="btn-danger" type="button" onClick={() => void revoke(row)}>
                        Revoke
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </article>
  )
}
