// Schedules hooks (docs/API.md "Schedules"). A module of their own so they
// load with the lazy screens that use them, not in the initial bundle.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, CSRF } from './client'
import { keys } from './keys'
import type { Schedule, ScheduleFiring, ScheduleInput } from './types'

export const scheduleKeys = {
  all: ['schedules'] as const,
  list: ['schedules', 'list'] as const,
  detail: (id: number) => ['schedules', 'detail', id] as const,
  firings: (id: number) => ['schedules', 'firings', id] as const,
}

export function useSchedules() {
  return useQuery({
    queryKey: scheduleKeys.list,
    queryFn: async ({ signal }) => (await call(api.GET('/api/v1/schedules', { signal }))).items,
    staleTime: 10_000,
  })
}

export function useSchedule(id: number | undefined) {
  return useQuery({
    queryKey: scheduleKeys.detail(id ?? 0),
    queryFn: ({ signal }) => call(api.GET('/api/v1/schedules/{id}', { params: { path: { id: id ?? 0 } }, signal })),
    enabled: id !== undefined,
  })
}

export function useScheduleFirings(id: number) {
  return useQuery({
    queryKey: scheduleKeys.firings(id),
    queryFn: async ({ signal }) =>
      (await call(api.GET('/api/v1/schedules/{id}/firings', { params: { path: { id }, query: { limit: 50 } }, signal }))).items,
  })
}

/** After any change: this schedule, the list and its history (a run also adds a task). */
function useScheduleResult() {
  const qc = useQueryClient()
  return (s?: Schedule) => {
    if (s) qc.setQueryData(scheduleKeys.detail(s.id), s)
    void qc.invalidateQueries({ queryKey: scheduleKeys.all })
  }
}

export function useCreateSchedule() {
  const done = useScheduleResult()
  return useMutation({
    mutationFn: (body: ScheduleInput) => call(api.POST('/api/v1/schedules', { params: { header: CSRF }, body })),
    onSuccess: done,
  })
}

export function useUpdateSchedule(id: number) {
  const done = useScheduleResult()
  return useMutation({
    mutationFn: (body: ScheduleInput) => call(api.PUT('/api/v1/schedules/{id}', { params: { path: { id }, header: CSRF }, body })),
    onSuccess: done,
  })
}

export function useDeleteSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => call(api.DELETE('/api/v1/schedules/{id}', { params: { path: { id }, header: CSRF } })),
    onSuccess: (_d, id) => {
      qc.removeQueries({ queryKey: scheduleKeys.detail(id) })
      void qc.invalidateQueries({ queryKey: scheduleKeys.list })
    },
  })
}

export function useSetScheduleEnabled() {
  const done = useScheduleResult()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
      enabled
        ? call(api.POST('/api/v1/schedules/{id}/enable', { params: { path: { id }, header: CSRF } }))
        : call(api.POST('/api/v1/schedules/{id}/disable', { params: { path: { id }, header: CSRF } })),
    onSuccess: done,
  })
}

export function useRunSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number): Promise<ScheduleFiring> =>
      call(api.POST('/api/v1/schedules/{id}/run', { params: { path: { id }, header: CSRF } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: scheduleKeys.all })
      void qc.invalidateQueries({ queryKey: keys.tasks.lists() })
    },
  })
}

export function useDuplicateSchedule() {
  const done = useScheduleResult()
  return useMutation({
    mutationFn: (id: number) => call(api.POST('/api/v1/schedules/{id}/duplicate', { params: { path: { id }, header: CSRF } })),
    onSuccess: done,
  })
}
