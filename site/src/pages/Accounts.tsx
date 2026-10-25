import { useState, type FormEvent } from 'react'
import { useLoaderData, useRevalidator } from 'react-router-dom'
import { api, guard, requireSession } from '../api'
import { Modal } from '../modal'
import { CopyButton } from '../ui'

type Account = {
  id: number
  username: string
  admin: boolean
  service: boolean
}

type AccountList = { accounts: Account[] }

export async function accountsLoader(): Promise<AccountList> {
  await requireSession()
  return guard(api<AccountList>('/dashboard/api/accounts'))
}

export function Accounts() {
  const data = useLoaderData() as AccountList
  const revalidator = useRevalidator()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [service, setService] = useState(false)
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  function close() {
    setOpen(false)
    setFormError('')
    setUsername('')
    setPassword('')
    setService(false)
  }

  async function create(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setFormError('')
    setToken('')
    try {
      const created = await api<{ token?: string }>('/dashboard/api/accounts', {
        method: 'POST',
        body: JSON.stringify({ username, password: service ? '' : password, service }),
      })
      setToken(created.token ?? '')
      close()
      revalidator.revalidate()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Could not create the account.')
    } finally {
      setBusy(false)
    }
  }

  async function remove(account: Account) {
    setError('')
    try {
      await api<void>(`/dashboard/api/accounts/${account.id}`, { method: 'DELETE' })
      revalidator.revalidate()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not delete the account.')
    }
  }

  async function rotate(account: Account) {
    setError('')
    setToken('')
    try {
      const created = await api<{ token: string }>(`/dashboard/api/accounts/${account.id}/tokens`, {
        method: 'POST',
        body: JSON.stringify({ name: 'default' }),
      })
      setToken(created.token)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not issue a token.')
    }
  }

  return (
    <article>
      <h1>Accounts</h1>
      <p className="lede">
        People sign in with a password. A service account has no dashboard password. Its token is the docker login password.
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
          Create account
        </button>
      </div>
      <Modal open={open} title="Create account" titleId="create-account-title" onClose={close}>
        <form className="modal-form" onSubmit={(event) => void create(event)}>
          <div className="field">
            <label htmlFor="account-name">Username</label>
            <input id="account-name" value={username} onChange={(event) => setUsername(event.target.value)} required />
          </div>
          {service ? null : (
            <div className="field">
              <label htmlFor="account-password">Password</label>
              <input
                id="account-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
                minLength={8}
              />
            </div>
          )}
          <label className="check">
            <input type="checkbox" checked={service} onChange={(event) => setService(event.target.checked)} />
            Service account
          </label>
          {formError ? (
            <p className="alert" role="alert">
              {formError}
            </p>
          ) : null}
          <button className="btn login-submit" type="submit" disabled={busy}>
            {service ? 'Create and show token' : 'Create account'}
          </button>
        </form>
      </Modal>
      <div className="panel section">
        <table>
          <thead>
            <tr>
              <th>Username</th>
              <th>Kind</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {data.accounts.map((account) => (
              <tr key={account.id}>
                <td data-label="Username">{account.username}</td>
                <td data-label="Kind">{account.admin ? 'Admin' : account.service ? 'Service' : 'User'}</td>
                <td data-label="Actions">
                  <div className="row-actions">
                    {account.service ? (
                      <button className="btn-ghost" type="button" onClick={() => void rotate(account)}>
                        New token
                      </button>
                    ) : null}
                    {account.admin ? null : (
                      <button className="btn-danger" type="button" onClick={() => void remove(account)}>
                        Delete
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </article>
  )
}
