import { useEffect } from 'react'
import {
  isRouteErrorResponse,
  Outlet,
  redirect,
  RouterProvider,
  createBrowserRouter,
  useLoaderData,
  useRouteError,
} from 'react-router-dom'
import { loadSession, type Session } from './api'
import { catalogLoader, Catalog } from './pages/Catalog'
import { Login } from './pages/Login'
import { RepoView, repoLoader, type RepoData } from './pages/Repo'
import { accountsLoader, Accounts } from './pages/Accounts'
import { storageLoader, Storage } from './pages/Storage'
import { tokensLoader, Tokens } from './pages/Tokens'
import { Shell } from './shell'

const basename = import.meta.env.BASE_URL.replace(/\/$/, '')

function Frame() {
  const session = useLoaderData() as Session | null
  useEffect(() => {
    document.title = 'Registry'
  }, [])
  return (
    <Shell username={session?.username ?? null} admin={session?.admin ?? false}>
      <Outlet />
    </Shell>
  )
}

function RepoPage() {
  const data = useLoaderData() as RepoData
  const title = data.kind === 'tags' ? data.tags.name : data.manifest.repo
  useEffect(() => {
    document.title = title
  }, [title])
  return <RepoView data={data} />
}

function Failed() {
  const error = useRouteError()
  const message = messageOf(error)
  return (
    <Shell username={null}>
      <h1 className="page-name">That page did not load.</h1>
      <p className="alert" role="alert">
        {message === 'not found' ? 'That repository or manifest is not in the registry.' : message}
      </p>
    </Shell>
  )
}

function messageOf(error: unknown): string {
  if (isRouteErrorResponse(error)) {
    if (error.status === 404) return 'not found'
    const data = typeof error.data === 'string' ? error.data : ''
    return data || error.statusText || 'The page failed to load.'
  }
  if (error instanceof Error) return error.message
  return 'The page failed to load.'
}

async function loginLoader(): Promise<null> {
  const session = await loadSession()
  if (session) throw redirect('/')
  return null
}

const router = createBrowserRouter(
  [
    {
      path: '/login',
      loader: loginLoader,
      element: <Login />,
      errorElement: <Failed />,
    },
    {
      path: '/',
      loader: () => loadSession(),
      element: <Frame />,
      errorElement: <Failed />,
      children: [
        { index: true, loader: catalogLoader, element: <Catalog /> },
        { path: 'gc', loader: storageLoader, element: <Storage /> },
        { path: 'tokens', loader: tokensLoader, element: <Tokens /> },
        { path: 'accounts', loader: accountsLoader, element: <Accounts /> },
        {
          path: 'repo/*',
          loader: repoLoader,
          element: <RepoPage />,
        },
      ],
    },
  ],
  { basename },
)

export function App() {
  return <RouterProvider router={router} />
}
