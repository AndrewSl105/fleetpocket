import { lazy, Suspense, useState } from 'react'
import { createBrowserRouter, RouterProvider } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from '@/components/ui/sonner'
import { ErrorBoundary, CrashScreen } from '@/components/ErrorBoundary'
import { AppShell } from '@/components/AppShell'
import { ConfirmProvider } from '@/components/confirm'
import { AuthGate } from '@/auth/AuthGate'
import { RealtimeProvider } from '@/realtime/RealtimeProvider'
import { RuntimeProvider, type Runtime } from '@/telegram/runtime'
import { ThemeProvider } from '@/lib/theme'
import { isApiError } from '@/api/errors'
import { TasksLayout } from '@/routes/tasks/TasksLayout'
import { NoTaskSelected } from '@/routes/tasks/NoTaskSelected'
import { NotFound } from '@/routes/NotFound'
import { PageSpinner } from '@/components/PageSpinner'

const TaskDetailPage = lazy(() => import('@/routes/tasks/TaskDetailPage'))
const NewTaskPage = lazy(() => import('@/routes/NewTaskPage'))
const ProjectsPage = lazy(() => import('@/routes/projects/ProjectsPage'))
const ProjectEditPage = lazy(() => import('@/routes/projects/ProjectEditPage'))
const SettingsPage = lazy(() => import('@/routes/SettingsPage'))
const IntegrationsPage = lazy(() => import('@/routes/integrations/IntegrationsPage'))
const CanvasPage = lazy(() => import('@/routes/canvas/CanvasPage'))
const CostsPage = lazy(() => import('@/routes/costs/CostsPage'))
const SkillsPage = lazy(() => import('@/routes/skills/SkillsPage'))
const SkillEditPage = lazy(() => import('@/routes/skills/SkillEditPage'))
// One chunk for the three Schedules screens.
const SchedulesPage = lazy(() => import('@/routes/schedules/screens').then((m) => ({ default: m.SchedulesPage })))
const ScheduleDetailPage = lazy(() => import('@/routes/schedules/screens').then((m) => ({ default: m.ScheduleDetailPage })))
const ScheduleEditPage = lazy(() => import('@/routes/schedules/screens').then((m) => ({ default: m.ScheduleEditPage })))
// One chunk for the three Ideas screens.
const IdeasPage = lazy(() => import('@/routes/ideas/screens').then((m) => ({ default: m.IdeasPage })))
const IdeaDetailPage = lazy(() => import('@/routes/ideas/screens').then((m) => ({ default: m.IdeaDetailPage })))
const IdeasArchivePage = lazy(() => import('@/routes/ideas/screens').then((m) => ({ default: m.IdeasArchivePage })))
// Only the first-run wizard needs it (and its QR code library); keep it out of the initial bundle.
// Local mode only (npx jungle-monkey): Docker, agent, a folder, GitHub, the first task.
const OnboardingPage = lazy(() => import('@/local/OnboardingPage'))
const SetupPage = lazy(() => import('@/setup/SetupPage').then((m) => ({ default: m.SetupPage })))

function lazyPage(Page: React.ComponentType) {
  return (
    <Suspense fallback={<PageSpinner />}>
      <Page />
    </Suspense>
  )
}

export function createAppRouter() {
  return createBrowserRouter([
    { path: '/welcome', element: lazyPage(OnboardingPage), errorElement: <CrashScreen /> },
    {
      path: '/',
      element: <AppShell />,
      errorElement: <CrashScreen />,
      children: [
        {
          element: <TasksLayout />,
          children: [
            { index: true, element: <NoTaskSelected /> },
            { path: 'tasks/:id', element: lazyPage(TaskDetailPage) },
          ],
        },
        { path: 'new', element: lazyPage(NewTaskPage) },
        { path: 'canvas', element: lazyPage(CanvasPage) },
        { path: 'projects', element: lazyPage(ProjectsPage) },
        { path: 'projects/new', element: lazyPage(ProjectEditPage) },
        { path: 'projects/:id', element: lazyPage(ProjectEditPage) },
        { path: 'projects/:id/integrations', element: lazyPage(IntegrationsPage) },
        { path: 'settings', element: lazyPage(SettingsPage) },
        { path: 'costs', element: lazyPage(CostsPage) },
        { path: 'skills', element: lazyPage(SkillsPage) },
        { path: 'skills/new', element: lazyPage(SkillEditPage) },
        { path: 'skills/:id', element: lazyPage(SkillEditPage) },
        { path: 'schedules', element: lazyPage(SchedulesPage) },
        { path: 'schedules/new', element: lazyPage(ScheduleEditPage) },
        { path: 'schedules/:id', element: lazyPage(ScheduleDetailPage) },
        { path: 'schedules/:id/edit', element: lazyPage(ScheduleEditPage) },
        { path: 'ideas', element: lazyPage(IdeasPage) },
        { path: 'ideas/archive', element: lazyPage(IdeasArchivePage) },
        { path: 'ideas/:id', element: lazyPage(IdeaDetailPage) },
        { path: '*', element: <NotFound /> },
      ],
    },
  ])
}

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: (count, e) => !(isApiError(e) && e.status < 500) && count < 2,
        refetchOnWindowFocus: true,
      },
      mutations: { retry: false },
    },
  })
}

export function App({ runtime }: { runtime: Runtime }) {
  const [queryClient] = useState(createQueryClient)
  const [router] = useState(createAppRouter)
  return (
    <ErrorBoundary>
      <RuntimeProvider runtime={runtime}>
        <ThemeProvider>
          <QueryClientProvider client={queryClient}>
            <ConfirmProvider>
              {/* The first-run wizard runs before an owner (and so a session) exists. */}
              {runtime.mode === 'browser' && window.location.pathname === '/setup' ? (
                <Suspense fallback={<PageSpinner />}>
                  <SetupPage />
                </Suspense>
              ) : (
                <AuthGate>
                  <RealtimeProvider>
                    <RouterProvider router={router} />
                  </RealtimeProvider>
                </AuthGate>
              )}
            </ConfirmProvider>
            <Toaster position="top-center" offset={{ top: 'calc(var(--safe-top) + 12px)' }} />
          </QueryClientProvider>
        </ThemeProvider>
      </RuntimeProvider>
    </ErrorBoundary>
  )
}
