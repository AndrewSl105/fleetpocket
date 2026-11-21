import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { resolveRuntime } from './telegram/runtime'
// Latin subsets only (plus Latin Extended for Figtree, for people's names):
// the UI is English, and every extra @font-face counts toward the initial CSS
// budget (scripts/check-size.mjs).
import '@fontsource/figtree/latin-400.css'
import '@fontsource/figtree/latin-600.css'
import '@fontsource/figtree/latin-ext-400.css'
import '@fontsource/figtree/latin-ext-600.css'
import '@fontsource/gloock/latin-400.css'
import '@fontsource/jetbrains-mono/latin-400.css'
import '@fontsource/jetbrains-mono/latin-600.css'
import './index.css'

async function enableMocks() {
  if (import.meta.env.VITE_MOCK !== '1') return
  const { startMocks } = await import('./mocks/browser')
  await startMocks()
}

void enableMocks().then(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App runtime={resolveRuntime()} />
    </StrictMode>,
  )
})

// Installable app (PWA): only in production builds; mock mode uses MSW's own worker.
if (import.meta.env.PROD && import.meta.env.VITE_MOCK !== '1' && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // Not installable then; the app works the same.
    })
  })
}
