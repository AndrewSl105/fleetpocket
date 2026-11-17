import { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { useMode, useProjects } from '@/api/hooks'
import { hasLocalRepo } from '@/lib/local'

// Local mode shows onboarding (/welcome) on the first screen until a project
// has a local repository. "Skip to the app" or starting the first task ends it
// for this page load; deep links are never redirected.

let dismissed = false

export function dismissOnboarding() {
  dismissed = true
}

export function shouldOnboard(mode: 'server' | 'local' | undefined, hasLocal: boolean | undefined, pathname: string): boolean {
  return mode === 'local' && hasLocal === false && pathname === '/' && !dismissed
}

/** Sends local mode's first screen to onboarding when there is no local repository yet. */
export function useLocalOnboardingRedirect() {
  const mode = useMode()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const projects = useProjects()
  const hasLocal = projects.data ? hasLocalRepo(projects.data) : undefined
  useEffect(() => {
    if (shouldOnboard(mode, hasLocal, pathname)) void navigate('/welcome', { replace: true })
  }, [mode, hasLocal, pathname, navigate])
}
