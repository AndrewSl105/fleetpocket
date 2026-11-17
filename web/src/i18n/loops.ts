// Strings of the loop UI (composer toggle, schedule editor, timeline). Kept
// out of en.ts so they load with the lazy screens that use them.

export const lp = {
  toggle: 'Loop until a check passes',
  short: 'Loop',
  toggleHint: 'After each run Jungle Monkey runs the check in the task container; if it fails, the agent gets its output and tries again. The PR opens once, at the end.',
  check: 'Check command',
  checkPlaceholder: 'npm test',
  checkRequired: 'Enter the command that exits 0 when the task is done',
  checkExamples: 'e.g. npm test, go test ./..., make lint, or a script that exits 0 when done',
  maxIterations: 'Max iterations',
  budget: 'Loop budget (USD)',
  budgetHint: 'All iterations together; empty: only the per-run budget',

  // Timeline
  iteration: (n: number, max: number) => `Iteration ${n}/${max}`,
  checkPassed: '✓ Check passed',
  checkFailed: (n: number, max: number) => `✕ Check failed ${n}/${max}`,
  checkTimedOut: 'timed out',
  exitCode: (code: number) => `exit ${code}`,
  showOutput: 'Output',
  hideOutput: 'Hide output',
  ended: (state: string) =>
    ({
      passed: 'Loop finished: the check passed',
      max_iterations: 'Loop stopped: max iterations reached',
      budget: 'Loop stopped: loop budget used up',
      no_changes: 'Loop stopped: two iterations without file changes',
      stopped: 'Loop stopped',
    })[state] ?? 'Loop stopped',
  stateStamp: (state: string) =>
    ({ running: 'Looping', passed: 'Check passed', max_iterations: 'Max iterations', budget: 'Loop budget', no_changes: 'No changes', stopped: 'Loop stopped' })[
      state
    ] ?? state,
}
