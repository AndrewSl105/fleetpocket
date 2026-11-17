// Which backend mode the mock pretends to be (docs/DECISIONS.md, Web).
// `?mock=local` (or `local-demo`, `server`) stores the choice in localStorage
// so it survives reloads; the default is the server mode.

export const MOCK_MODE_KEY = 'fleetpocket.mock.mode'

export type MockMode = 'server' | 'local' | 'local-demo'

export function mockMode(): MockMode {
  try {
    const v = localStorage.getItem(MOCK_MODE_KEY)
    return v === 'local' || v === 'local-demo' ? v : 'server'
  } catch {
    return 'server'
  }
}

export function isLocalMock(): boolean {
  return mockMode() !== 'server'
}

/** Applies `?mock=` from the URL, if present. */
export function applyMockModeParam(search: string) {
  const v = new URLSearchParams(search).get('mock')
  if (v !== 'local' && v !== 'local-demo' && v !== 'server') return
  try {
    localStorage.setItem(MOCK_MODE_KEY, v)
  } catch {
    // ignore
  }
}
