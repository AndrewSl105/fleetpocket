import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { FolderGit2, ListTodo, Plus, Settings, SquareTerminal } from 'lucide-react'
import { lazy, Suspense } from 'react'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'
import { haptic } from '@/telegram/haptics'
import { useBackButton } from '@/telegram/controls'
import { DeepLink } from '@/telegram/DeepLinkHandler'
import { Logo } from './Logo'
import { OfflineBanner } from './OfflineBanner'

const DebugPanel = import.meta.env.VITE_DEBUG === '1' ? lazy(() => import('@/telegram/DebugPanel')) : null

const TAB_ROOTS = ['/', '/new', '/canvas', '/projects', '/settings']

const NAV = [
  { to: '/', label: t.nav.tasks, icon: ListTodo, end: false },
  { to: '/new', label: t.nav.new, icon: Plus, end: true, primary: true },
  { to: '/projects', label: t.nav.projects, icon: FolderGit2, end: false },
  { to: '/settings', label: t.nav.settings, icon: Settings, end: true },
] as const

/** The sidebar adds the canvas, a desktop view (not a mobile tab). */
const SIDEBAR_NAV = [NAV[0], { to: '/canvas', label: t.nav.canvas, icon: SquareTerminal, end: true }, ...NAV.slice(1)] as const

function isActive(pathname: string, to: string) {
  if (to === '/') return pathname === '/' || pathname.startsWith('/tasks')
  return pathname === to || pathname.startsWith(`${to}/`)
}

export function parentPath(pathname: string): string {
  if (pathname.startsWith('/projects/')) return '/projects'
  return '/'
}

export function AppShell() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const deep = !TAB_ROOTS.includes(pathname)

  useBackButton(deep, () => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0
    if (idx > 0) void navigate(-1)
    else void navigate(parentPath(pathname))
  })

  return (
    <div className="flex h-(--app-height) overflow-hidden bg-background pt-(--safe-top)">
      <DeepLink />
      {/* Desktop sidebar: a Start menu that stays open */}
      <aside className="win-raised hidden w-56 shrink-0 p-[3px] lg:flex">
        <div className="win-titlebar flex w-6 shrink-0 items-end justify-center pb-2">
          <span className="text-base tracking-wide whitespace-nowrap [writing-mode:vertical-rl] rotate-180">
            {t.app.name} <span className="font-normal">98</span>
          </span>
        </div>
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex items-center gap-2 px-2 py-3">
            <Logo className="size-7" />
            <span className="font-bold">{t.app.name}</span>
          </div>
          <div className="win-groove mx-1 mb-1 h-0.5" />
          <nav className="flex flex-col" aria-label="Main">
            {SIDEBAR_NAV.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                className={cn(
                  'flex h-10 items-center gap-3 px-3 text-sm',
                  isActive(pathname, item.to)
                    ? 'bg-(--win-select) text-(--win-select-fg)'
                    : 'text-foreground hover:bg-(--win-select) hover:text-(--win-select-fg)',
                )}
              >
                <item.icon className="size-5" aria-hidden="true" />
                {item.label}
              </NavLink>
            ))}
          </nav>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <OfflineBanner />
        <div className="min-h-0 flex-1">
          <Outlet />
        </div>
        {/* Mobile bottom tabs, only on tab roots: the taskbar, with New as the Start button */}
        {!deep && (
          <nav
            aria-label="Main"
            className="win-raised grid shrink-0 grid-cols-4 gap-1 px-1 pt-1 pb-[calc(var(--safe-bottom)+4px)] lg:hidden"
          >
            {NAV.map((item) => {
              const active = isActive(pathname, item.to)
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={() => haptic.select()}
                  className={cn(
                    'flex min-h-11 items-center justify-center gap-1 px-1 text-[12px] text-foreground',
                    active ? 'win-pressed bg-muted font-bold' : 'win-raised',
                  )}
                >
                  {'primary' in item ? (
                    <>
                      <Logo className="size-4 shrink-0" />
                      <span className="font-bold">{item.label}</span>
                    </>
                  ) : (
                    <>
                      <item.icon className="size-4 shrink-0" aria-hidden="true" />
                      <span className="truncate">{item.label}</span>
                    </>
                  )}
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
