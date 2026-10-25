import http from 'node:http'
import type { Plugin } from 'vite'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Node's URL parser treats the host "[::1]" as a hostname, so Vite's proxy
// string target cannot reach the registry. 127.0.0.1:5000 is often AirPlay.
const registryHost = process.env.REGISTRY_DEV_HOST ?? '::1'
const registryPort = Number(process.env.REGISTRY_DEV_PORT ?? '5000')

function registryProxy(): Plugin {
  return {
    name: 'registry-proxy',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const path = req.url ?? ''
        const proxied = path.startsWith('/dashboard/api') || path === '/healthz' || path === '/readyz'
        if (!proxied) {
          next()
          return
        }
        const proxyReq = http.request(
          {
            hostname: registryHost,
            port: registryPort,
            path,
            method: req.method,
            headers: { ...req.headers, host: `[${registryHost}]:${registryPort}` },
          },
          (proxyRes) => {
            res.writeHead(proxyRes.statusCode ?? 502, proxyRes.headers)
            proxyRes.pipe(res)
          },
        )
        proxyReq.on('error', () => {
          if (!res.headersSent) res.statusCode = 502
          res.end('registry unavailable')
        })
        req.pipe(proxyReq)
      })
    },
  }
}

export default defineConfig({
  base: '/dashboard/',
  plugins: [react(), registryProxy()],
  build: {
    outDir: 'out',
    emptyOutDir: true,
  },
  server: {
    port: 5174,
    strictPort: true,
  },
})
