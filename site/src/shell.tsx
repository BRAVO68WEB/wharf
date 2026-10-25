import { useEffect, useState, type ReactNode } from 'react'
import { NavLink, useNavigate, useNavigation } from 'react-router-dom'
import { logout } from './api'
import { applyTheme, readTheme, storeTheme, type ThemeChoice } from './theme'

export function Shell({
  username,
  admin = false,
  children,
}: {
  username: string | null
  admin?: boolean
  children: ReactNode
}) {
  const navigation = useNavigation()
  return (
    <div className="app">
      <a className="skip" href="#work">
        Skip to content
      </a>
      <header className="topbar">
        <div className="topbar-start">
          <NavLink className="brand" to="/" end>
            <Mark />
            Registry
          </NavLink>
          <nav aria-label="Dashboard">
            <NavLink to="/" end>
              Repositories
            </NavLink>
            {username ? <NavLink to="/gc">Storage</NavLink> : null}
            {username ? <NavLink to="/tokens">Tokens</NavLink> : null}
            {admin ? <NavLink to="/accounts">Accounts</NavLink> : null}
          </nav>
        </div>
        <div className="topbar-end">
          <ThemeToggle />
          {username ? (
            <>
              <span className="who">{username}</span>
              <SignOut />
            </>
          ) : (
            <NavLink className="signin" to="/login">
              Sign in
            </NavLink>
          )}
        </div>
      </header>
      <main id="work" className="page" aria-busy={navigation.state === 'loading'}>
        <div className="page-inner">{children}</div>
      </main>
    </div>
  )
}

function ThemeToggle() {
  const [choice, setChoice] = useState<ThemeChoice>(readTheme)
  const [systemDark, setSystemDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches)
  const dark = choice === 'dark' || (choice === 'system' && systemDark)

  useEffect(() => {
    applyTheme(choice)
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => setSystemDark(media.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [choice])

  return (
    <button
      type="button"
      className="theme-btn"
      aria-pressed={dark}
      aria-label={dark ? 'Use light theme' : 'Use dark theme'}
      onClick={() => {
        const next: ThemeChoice = dark ? 'light' : 'dark'
        storeTheme(next)
        setChoice(next)
      }}
    >
      {dark ? <Sun /> : <Moon />}
    </button>
  )
}

function Moon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M9.2 1.6a6.2 6.2 0 1 0 5.2 9.4 5.2 5.2 0 0 1-6.6-7.8 5 5 0 0 1 1.4-1.6Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function Sun() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <circle cx="8" cy="8" r="2.4" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path
        d="M8 1.6v1.6M8 12.8v1.6M1.6 8h1.6M12.8 8h1.6M3.4 3.4l1.1 1.1M11.5 11.5l1.1 1.1M12.6 3.4l-1.1 1.1M4.5 11.5l-1.1 1.1"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  )
}

function SignOut() {
  const navigate = useNavigate()
  const [error, setError] = useState('')
  return (
    <>
      <button
        type="button"
        className="text-btn"
        onClick={() => {
          void logout()
            .then(() => navigate('/login'))
            .catch((err: unknown) => {
              setError(err instanceof Error ? err.message : 'Sign-out failed.')
            })
        }}
      >
        Sign out
      </button>
      {error ? (
        <span className="alert" role="alert">
          {error}
        </span>
      ) : null}
    </>
  )
}

function Mark() {
  return (
    <svg className="mark" viewBox="0 0 24 24" aria-hidden="true">
      <rect x="2" y="9" width="5" height="4" rx="0.6" fill="currentColor" />
      <rect x="8.5" y="9" width="5" height="4" rx="0.6" fill="currentColor" />
      <rect x="15" y="9" width="5" height="4" rx="0.6" fill="currentColor" />
      <rect x="8.5" y="14.5" width="5" height="4" rx="0.6" fill="currentColor" />
      <path d="M18.2 7.2c1.6.3 2.8 1.6 2.8 3.3v1.2h-2.2V10c0-.7-.5-1.2-1.2-1.2h-.6V7.2h1.2Z" fill="currentColor" />
    </svg>
  )
}
