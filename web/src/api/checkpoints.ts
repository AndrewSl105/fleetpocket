// Checkpoint hooks (docs/API.md "Checkpoints"). A module of their own so they
// load with the task screen and settings, not in the initial bundle.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, CSRF } from './client'
import { keys } from './keys'
import { upsertTask } from './cache'
import type { CheckpointSettings, TaskView } from './types'

const checkpointKeys = {
  settings: ['settings', 'checkpoints'] as const,
  list: (taskId: number) => ['tasks', 'checkpoints', taskId] as const,
  preview: (taskId: number, id: number) => ['tasks', 'checkpoints', taskId, id] as const,
}

/** The task's checkpoints; only fetched when its timeline shows one. */
export function useTaskCheckpoints(taskId: number, enabled = true) {
  return useQuery({
    queryKey: checkpointKeys.list(taskId),
    queryFn: ({ signal }) => call(api.GET('/api/v1/tasks/{id}/checkpoints', { params: { path: { id: taskId } }, signal })),
    enabled,
    staleTime: 10_000,
  })
}

/** What restoring a checkpoint discards (counts and the file diff). */
export function useCheckpointPreview(taskId: number, id: number, enabled: boolean) {
  return useQuery({
    queryKey: checkpointKeys.preview(taskId, id),
    queryFn: ({ signal }) =>
      call(api.GET('/api/v1/tasks/{id}/checkpoints/{checkpointId}', { params: { path: { id: taskId, checkpointId: id } }, signal })),
    enabled,
    staleTime: 0,
  })
}

export function useRestoreCheckpoint(taskId: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, instruction }: { id: number; instruction: string }) =>
      call(
        api.POST('/api/v1/tasks/{id}/checkpoints/{checkpointId}/restore', {
          params: { path: { id: taskId, checkpointId: id }, header: CSRF },
          body: { instruction },
        }),
      ),
    onSuccess: (task: TaskView) => {
      qc.setQueryData(keys.tasks.detail(task.id), task)
      upsertTask(qc, task)
      void qc.invalidateQueries({ queryKey: ['tasks', 'checkpoints', taskId] })
    },
  })
}

export function useCheckpointSettings() {
  return useQuery({
    queryKey: checkpointKeys.settings,
    queryFn: ({ signal }) => call(api.GET('/api/v1/settings/checkpoints', { signal })),
    staleTime: 60_000,
  })
}

export function useSaveCheckpointSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CheckpointSettings) => call(api.PUT('/api/v1/settings/checkpoints', { params: { header: CSRF }, body })),
    onSuccess: (data: CheckpointSettings) => qc.setQueryData(checkpointKeys.settings, data),
  })
}
