// Strings of the checkpoint UI (timeline marker, restore dialog, setting).
// Kept out of en.ts so they load with the lazy screens that use them.

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

export const cp = {
  marker: (n: number, reason: string, files: number) =>
    reason === 'question' ? `Checkpoint ${n} · before a question` : `Checkpoint ${n} · ${plural(files, 'file')} changed`,
  restoreHere: 'Restore here',
  unavailable: 'Restore unavailable',
  abandoned: (n: number) => `Abandoned branch · ${plural(n, 'entry', 'entries')}`,
  showAbandoned: 'Show',
  hideAbandoned: 'Hide',
  restored: (n: number, mode: string) =>
    `Restored to checkpoint ${n} · ${mode === 'fork' ? 'session forked there' : 'new session from a summary'}`,
  forcePushed: (branch: string, from: string) => `Rewrote ${branch} with --force-with-lease (was ${from.slice(0, 7)})`,
  dialog: {
    title: (n: number) => `Restore checkpoint ${n}?`,
    description:
      'The task branch goes back to this point and the agent continues from here. Later steps stay in the timeline as an abandoned branch.',
    loading: 'Checking what changes…',
    discards: 'This discards',
    discarded: (d: { runs: number; steps: number; messages: number; commits: number }) =>
      [plural(d.runs, 'later run'), plural(d.steps, 'step'), plural(d.messages, 'message'), plural(d.commits, 'commit')].join(' · '),
    files: (n: number) => (n === 0 ? 'No file changes after this point' : `${plural(n, 'file')} changed after this point`),
    truncated: 'Diff truncated',
    fork: 'Claude resumes its session at this point (a fork).',
    summary: 'This agent cannot resume at a point: a new session starts with a summary of the steps up to here.',
    forcePush: 'Commits already pushed after this point will be replaced on the agent branch (force-with-lease, noted on the PR).',
    instruction: 'Then (optional)',
    instructionPlaceholder: '…then do it with a migration instead',
    confirm: 'Restore and continue',
    cancel: 'Cancel',
    restoring: 'Restoring…',
    done: 'Restored: the task continues from the checkpoint',
  },
  settings: {
    title: 'Checkpoints',
    hint: 'Save the working tree after every agent step that changes files and before every question, so a task can be rolled back to a step and continued. Kept as long as the workspace (WORKSPACE_RETENTION); never pushed.',
    enabled: 'Take checkpoints',
    saved: 'Saved',
  },
}
