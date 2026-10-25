import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { APIError, login } from '../api'
import { Shell } from '../shell'

export function Login() {
  const navigate = useNavigate()
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const [showPassword, setShowPassword] = useState(false)

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const username = String(data.get('username') ?? '')
    const password = String(data.get('password') ?? '')
    setPending(true)
    setError('')
    void login(username, password)
      .then(() => navigate('/'))
      .catch((err: unknown) => {
        if (err instanceof APIError && err.status === 401) {
          setError('That username or password is wrong.')
        } else {
          setError(err instanceof Error ? err.message : 'Sign-in failed.')
        }
      })
      .finally(() => setPending(false))
  }

  return (
    <Shell username={null}>
      <form className="login-wrap" onSubmit={onSubmit}>
        <h1>Sign in</h1>
        <p className="lede">The same account opens the dashboard and docker login.</p>
        <div className="field">
          <label htmlFor="username">Username</label>
          <input id="username" name="username" autoComplete="username" autoFocus required />
        </div>
        <div className="field">
          <label htmlFor="password">Password</label>
          <div className="password-field">
            <input
              id="password"
              name="password"
              type={showPassword ? 'text' : 'password'}
              autoComplete="current-password"
              required
            />
            <button
              className="password-toggle"
              type="button"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
              aria-pressed={showPassword}
              onClick={() => setShowPassword((value) => !value)}
            >
              <Eye off={showPassword} />
            </button>
          </div>
        </div>
        {error ? (
          <p className="alert" role="alert">
            {error}
          </p>
        ) : null}
        <button className="btn login-submit" type="submit" disabled={pending}>
          {pending ? 'Signing in' : 'Sign in'}
        </button>
      </form>
    </Shell>
  )
}

function Eye({ off }: { off: boolean }) {
  return (
    <svg className="eye" viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M1.5 8S3.8 3.8 8 3.8 14.5 8 14.5 8 12.2 12.2 8 12.2 1.5 8 1.5 8Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" strokeWidth="1.4" />
      {off ? <path d="M3.2 12.8 12.8 3.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /> : null}
    </svg>
  )
}
