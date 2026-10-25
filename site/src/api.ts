import { redirect } from 'react-router-dom'

export type Session = {
  username: string
  csrfToken: string
  admin: boolean
}

export type Repo = {
  name: string
  tagCount: number
  manifestCount: number
  sizeBytes: number
  updatedAt: string
  digest?: string
  published: boolean
  latestTag?: string
  pullHost?: string
}

export type RepoList = {
  search: string
  page: number
  pageSize: number
  total: number
  mounted: Repo | null
  repos: Repo[]
}

export type Tag = {
  name: string
  digest: string
  size: number
  updatedAt: string
}

export type TagList = {
  name: string
  pullHost?: string
  tags: Tag[]
}

export type Layer = {
  digest: string
  size: number
  mediaType?: string
}

export type ImageLayer = {
  command: string
  size: number
  digest?: string
}

export type ImageConfig = {
  cmd?: string[]
  entrypoint?: string[]
  env?: string[]
  workingDir?: string
  exposedPorts?: string[]
  user?: string
}

export type Manifest = {
  repo: string
  digest: string
  mediaType: string
  size: number
  configDigest?: string
  layers: Layer[]
  imageLayers: ImageLayer[]
  config?: ImageConfig
}

export type GCStats = {
  totalBlobs: number
  referencedBlobs: number
  unreferencedBlobs: number
  unreferencedBytes: number
  intervalSeconds: number
  lastRunAt?: string
}

export type GCBlob = {
  digest: string
  size: number
}

export type GCResult = {
  stats: GCStats
  blobs: GCBlob[]
  bytes: number
  failed: number
  ran: boolean
}

export class APIError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
  }
}

let session: Session | null = null
let known = false
let inflight: Promise<Session | null> | null = null

export function currentSession(): Session | null {
  return session
}

export function clearSession(): void {
  session = null
  known = false
}

export function loadSession(): Promise<Session | null> {
  if (known) return Promise.resolve(session)
  if (!inflight) {
    inflight = fetch('/dashboard/api/session', { credentials: 'same-origin' })
      .then(async (res) => {
        if (res.status === 401) {
          session = null
          known = true
          return null
        }
        if (!res.ok) throw await readError(res)
        session = (await res.json()) as Session
        known = true
        return session
      })
      .finally(() => {
        inflight = null
      })
  }
  return inflight
}

export async function requireSession(): Promise<Session> {
  const current = await loadSession()
  if (!current) throw redirect('/login')
  return current
}

export async function login(username: string, password: string): Promise<Session> {
  const res = await fetch('/dashboard/api/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (!res.ok) throw await readError(res)
  session = (await res.json()) as Session
  known = true
  return session
}

export async function logout(): Promise<void> {
  await api<void>('/dashboard/api/logout', { method: 'POST' })
  clearSession()
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const method = (init.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && session) {
    headers.set('X-CSRF-Token', session.csrfToken)
  }
  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (res.status === 401) {
    clearSession()
    throw new APIError(401, 'unauthenticated')
  }
  if (!res.ok) throw await readError(res)
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export async function guard<T>(work: Promise<T>): Promise<T> {
  try {
    return await work
  } catch (err) {
    if (err instanceof APIError && err.status === 401) throw redirect('/login')
    throw err
  }
}

async function readError(res: Response): Promise<APIError> {
  let message = res.statusText || 'The request failed.'
  try {
    const body = (await res.json()) as { error?: string }
    if (body.error) message = body.error
  } catch {
    // The body was not JSON. The status text stands.
  }
  return new APIError(res.status, message)
}

export function repoAPI(name: string): string {
  return '/dashboard/api/repo/' + name.split('/').map(encodeURIComponent).join('/')
}
