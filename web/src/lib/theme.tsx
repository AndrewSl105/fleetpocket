import { createContext, useContext, useEffect, type ReactNode } from 'react'

/**
 * The app is dark only: a stage at night (docs/SPEC-web.md section 7). There is
 * no light theme and no theme setting; Telegram's light scheme is not followed
 * either (docs/DECISIONS.md, Web).
 */
type ThemeState = { resolved: 'dark' }

const ThemeContext = createContext<ThemeState>({ resolved: 'dark' })

export function ThemeProvider({ children }: { children: ReactNode }) {
  useEffect(() => {
    document.documentElement.classList.add('dark')
    document.documentElement.style.colorScheme = 'dark'
  }, [])
  return <ThemeContext.Provider value={{ resolved: 'dark' }}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  return useContext(ThemeContext)
}
