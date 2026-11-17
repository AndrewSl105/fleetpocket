import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { BookOpen, FolderGit2, ListTodo, Plus, Receipt, Settings, SquareTerminal } from 'lucide-react'
import { lazy, Suspense } from 'react'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'
import { haptic } from '@/telegram/haptics'
import { useBackButton } from '@/telegram/controls'
import { DeepLink } from '@/telegram/DeepLinkHandler'
import { Logo } from './Logo'
import { OfflineBanner } from './OfflineBanner'
import { useLocalOnboardingRedirect } from '@/local/onboarding'

const DebugPanel = import.meta.env.VITE_DEBUG === '1' ? lazy(() => import('@/telegram/DebugPanel')) : null

const TAB_ROOTS = ['/', '/new', '/canvas', '/projects', '/settings']

const NAV = [
  { to: '/', label: t.nav.tasks, icon: ListTodo, end: false },
  { to: '/new', label: t.nav.new, icon: Plus, end: true, primary: true },
  { to: '/projects', label: t.nav.projects, icon: FolderGit2, end: false },
  { to: '/settings', label: t.nav.settings, icon: Settings, end: true },
] as const

/** The sidebar adds the canvas, a desktop view, Skills and Costs (on phones: from Settings and Projects). */
const SIDEBAR_NAV = [
  NAV[0],
  { to: '/canvas', label: t.nav.canvas, icon: SquareTerminal, end: true },
  ...NAV.slice(1, 3),
  { to: '/skills', label: t.nav.skills, icon: BookOpen, end: false },
  { to: '/costs', label: t.nav.costs, icon: Receipt, end: true },
  NAV[3],
] as const

function isActive(pathname: string, to: string) {
  if (to === '/') return pathname === '/' || pathname.startsWith('/tasks')
  return pathname === to || pathname.startsWith(`${to}/`)
}

export function parentPath(pathname: string): string {
  if (pathname.startsWith('/projects/')) return '/projects'
  if (pathname.startsWith('/skills/')) return '/skills'
  return '/'
}

export function AppShell() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const deep = !TAB_ROOTS.includes(pathname)
  useLocalOnboardingRedirect()

  useBackButton(deep, () => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0
    if (idx > 0) void navigate(-1)
    else void navigate(parentPath(pathname))
  })

  return (
    <div className="flex h-(--app-height) overflow-hidden bg-background pt-(--safe-top)">
      <DeepLink />
      {/* Desktop: a narrow ruled rail */}
      <aside className="hidden w-[184px] shrink-0 flex-col border-r-2 border-ink bg-background lg:flex">
        <div className="flex items-center gap-2.5 border-b-2 border-ink px-3 py-3">
          <Logo className="size-9" />
          <span className="fm-label leading-tight font-semibold">{t.app.name}</span>
        </div>
        <div className="border-b border-rule p-3">
          <NavLink to="/new" className="fm-btn-signal flex h-11 items-center justify-center gap-2 text-sm">
            <Plus className="size-4" strokeWidth={2} aria-hidden="true" />
            {t.nav.new}
          </NavLink>
        </div>
        <nav className="flex min-h-0 flex-1 flex-col" aria-label="Main">
          {SIDEBAR_NAV.filter((item) => !('primary' in item)).map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={cn(
                'fm-label flex h-11 items-center gap-3 border-b border-rule px-3 text-[12px]',
                isActive(pathname, item.to) ? 'fm-selected font-semibold' : 'text-foreground hover:bg-grid',
              )}
            >
              <item.icon className="size-4" strokeWidth={2} aria-hidden="true" />
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <OfflineBanner />
        <div className="min-h-0 flex-1">
          <Outlet />
        </div>
        {/* Mobile bottom tabs, only on tab roots. New lives in the page header. */}
        {!deep && (
          <nav aria-label="Main" className="fm-bar grid shrink-0 grid-cols-3 pb-(--safe-bottom) lg:hidden">
            {NAV.filter((item) => !('primary' in item)).map((item) => {
              const active = isActive(pathname, item.to)
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={() => haptic.select()}
                  aria-current={active ? 'page' : undefined}
                  className="fm-bar-item flex h-14 items-center justify-center border-r border-rule last:border-r-0"
                >
                  {item.label}
                </NavLink>
              )
            })}
          </nav>
        )}
      </div>
      {DebugPanel && (
        <Suspense>
          <DebugPanel />
        </Suspense>
      )}
    </div>
  )
}
