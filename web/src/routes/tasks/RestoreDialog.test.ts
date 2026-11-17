import { describe, expect, it } from 'vitest'
import { diffFiles } from './RestoreDialog'

describe('diffFiles', () => {
  it('lists files with added and removed lines', () => {
    const diff = ['diff --git a/x.sql b/x.sql', '--- a/x.sql', '+++ b/x.sql', '@@ -1 +1,2 @@', '-old', '+new', '+more', 'diff --git a/y.ts b/y.ts', '+++ b/y.ts', '+a'].join('\n')
    expect(diffFiles(diff)).toEqual([
      { path: 'x.sql', add: 2, del: 1 },
      { path: 'y.ts', add: 1, del: 0 },
    ])
    expect(diffFiles('')).toEqual([])
  })
})
