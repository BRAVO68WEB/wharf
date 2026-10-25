import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { applyTheme, readTheme } from './theme'
import '@fontsource/roboto/400.css'
import '@fontsource/roboto/500.css'
import '@fontsource/roboto/700.css'
import '@fontsource/roboto-mono/400.css'
import { App } from './App'
import './styles.css'

applyTheme(readTheme())

const root = document.getElementById('root')
if (!root) throw new Error('missing #root')

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
