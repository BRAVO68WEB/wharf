const when = new Intl.DateTimeFormat('en-US', {
  month: 'long',
  day: 'numeric',
  year: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
})

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return 'missing'
  const unit = 1024
  if (n < unit) return `${n} B`
  const marks = ['K', 'M', 'G', 'T', 'P', 'E']
  let value = n
  let exp = -1
  while (value >= unit && exp < marks.length - 1) {
    value /= unit
    exp++
  }
  return `${value.toFixed(1)} ${marks[exp]}B`
}

export function formatWhen(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return when.format(date)
}

export function repoPath(name: string): string {
  return '/repo/' + name.split('/').map(encodeURIComponent).join('/')
}

export function manifestPath(name: string, digest: string): string {
  return `${repoPath(name)}/manifests/${encodeURIComponent(digest)}`
}

export function decodeSplat(splat: string): { name: string; digest?: string } {
  const marker = '/manifests/'
  const at = splat.lastIndexOf(marker)
  const rawName = at >= 0 ? splat.slice(0, at) : splat
  const name = rawName.split('/').filter(Boolean).map(safeDecode).join('/')
  if (at < 0) return { name }
  return { name, digest: safeDecode(splat.slice(at + marker.length)) }
}

function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

export function pullCommand(host: string, repo: string, tag: string): string {
  return `docker pull ${host}/${repo}:${tag}`
}

export function shortDigest(digest: string): string {
  const [algo, hex] = digest.split(':')
  if (!hex || hex.length <= 19) return digest
  return `${algo}:${hex.slice(0, 12)}`
}

export function displayCommand(command: string): string {
  return command.replace(/\s+/g, ' ').trim()
}

export function pageCount(total: number, pageSize: number): number {
  if (pageSize <= 0 || total <= 0) return 1
  return Math.ceil(total / pageSize)
}
