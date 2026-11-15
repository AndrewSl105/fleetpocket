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
      <aside className="hidden w-60 shrink-0 flex-col overflow-hidden rounded-tr-[8px] bg-(--win-frame) p-[3px] pl-0 lg:flex">
        <div className="win-titlebar flex items-center gap-2.5 px-2.5 py-2.5">
          <span className="flex size-10 shrink-0 items-center justify-center rounded-[3px] border-2 border-white/80 bg-white shadow-[1px_1px_2px_rgb(0_0_0/0.4)]">
            <Logo className="size-7" />
          </span>
          <span className="truncate text-[15px]">{t.app.name}</span>
        </div>
        <div className="h-0.5 shrink-0 bg-[linear-gradient(90deg,transparent,#da884a_50%,transparent)]" aria-hidden="true" />
        <nav className="flex min-h-0 flex-1 flex-col bg-field py-1.5" aria-label="Main">
          {SIDEBAR_NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={cn(
                'mx-1 flex h-10 items-center gap-3 rounded-[2px] px-2.5 text-sm',
                isActive(pathname, item.to)
                  ? 'win-selected font-bold'
                  : 'text-foreground hover:bg-(--win-select) hover:text-(--win-select-fg)',
              )}
            >
              <item.icon className="size-5" aria-hidden="true" />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="win-taskbar flex h-9 shrink-0 items-center justify-end px-3 text-xs" aria-hidden="true">
          <span className="font-(family-name:--font-title) italic">
            {t.app.name}
          </span>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <OfflineBanner />
        <div className="min-h-0 flex-1">
          <Outlet />
        </div>
        {/* Mobile bottom tabs, only on tab roots: the taskbar, with New as the green Start button */}
        {!deep && (
          <nav
            aria-label="Main"
            className="win-taskbar grid shrink-0 grid-cols-4 gap-1 px-1 pt-1 pb-[calc(var(--safe-bottom)+4px)] lg:hidden"
          >
            {NAV.map((item) => {
              const active = isActive(pathname, item.to)
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={() => haptic.select()}
                  className={cn(
                    'flex min-h-11 items-center justify-center gap-1 px-1 text-[12px]',
                    'primary' in item ? 'win-start rounded-[10px] text-[15px]' : 'win-task',
                    active && 'font-bold',
                  )}
                >
                  {'primary' in item ? (
                    <>
                      <Logo className="size-5 shrink-0 text-white drop-shadow-[1px_1px_0_rgb(0_0_0/0.4)]" />
                      <span>{item.label}</span>
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
