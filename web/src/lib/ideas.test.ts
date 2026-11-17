import { describe, expect, it } from 'vitest'
import type { Idea } from '@/api/types'
import { afterIdAt, byColumn, formatDuration, ideaPrompt, parseTags, recorderMime, splitLinks, tagsOf } from './ideas'
import { placeCard } from '@/api/ideas'

const idea = (id: number, column: string, extra: Partial<Idea> = {}): Idea => ({
  id,
  column,
  project_id: null,
  title: `Idea ${id}`,
  body: '',
  position: id,
  tags: [],
  auto_note: null,
  source: 'web',
  archived_at: null,
  created_at: '2026-11-17T09:00:00Z',
  updated_at: '2026-11-17T09:00:00Z',
  attachments: [],
  voice_notes: [],
  linked_task_ids: [],
  linked_tasks: [],
  failed: false,
  ...extra,
})

describe('ideas helpers', () => {
  it('formats durations', () => {
    expect(formatDuration(4200)).toBe('0:04')
    expect(formatDuration(65_000)).toBe('1:05')
    expect(formatDuration(null)).toBe('0:00')
  })

  it('groups cards by column and finds the card a drop goes after', () => {
    const list = [idea(1, 'inbox'), idea(2, 'inbox'), idea(3, 'ready'), idea(4, 'gone')]
    const cols = byColumn(list, [
      { id: 'inbox', name: 'Inbox' },
      { id: 'ready', name: 'Ready' },
    ])
    expect(cols.get('inbox')?.map((i) => i.id)).toEqual([1, 2])
    expect(cols.has('gone')).toBe(false)
    const inbox = cols.get('inbox') ?? []
    expect(afterIdAt(inbox, 3, 0)).toBeNull()
    expect(afterIdAt(inbox, 3, 1)).toBe(1)
    expect(afterIdAt(inbox, 3, 9)).toBe(2)
    // Moving a card down inside its own column ignores the card itself.
    expect(afterIdAt(inbox, 1, 1)).toBe(2)
  })

  it('places a moved card like the server', () => {
    const list = [idea(1, 'inbox'), idea(2, 'inbox'), idea(3, 'ready')]
    expect(placeCard(list, 3, 'inbox', null).map((i) => i.id)).toEqual([3, 1, 2])
    expect(placeCard(list, 1, 'inbox', 2).map((i) => i.id)).toEqual([2, 1, 3])
    expect(placeCard(list, 1, 'ready', 3).find((i) => i.id === 1)?.column).toBe('ready')
  })

  it('parses tags and links', () => {
    expect(parseTags('ui, Perf ,, #ux, ui')).toEqual(['ui', 'perf', 'ux'])
    expect(tagsOf([idea(1, 'a', { tags: ['b', 'a'] }), idea(2, 'a', { tags: ['a'] })])).toEqual(['a', 'b'])
    expect(splitLinks('see https://example.com/x and http://a.b')).toEqual({ text: 'see and', links: ['https://example.com/x', 'http://a.b'] })
  })

  it('builds the task prompt from text, transcripts and links', () => {
    const i = idea(1, 'inbox', {
      title: 'Dark mode',
      body: 'For the canvas.',
      voice_notes: [
        { id: 1, kind: 'voice', name: 'v', mime: 'audio/webm', size: 1, width: 0, height: 0, duration_ms: 1000, transcript: 'Users asked twice.', url: null, title: null, has_thumb: false, created_at: '' },
      ],
      attachments: [
        { id: 2, kind: 'link', name: 'x', mime: '', size: 0, width: 0, height: 0, duration_ms: null, transcript: null, url: 'https://x.dev', title: 'Spec', has_thumb: false, created_at: '' },
      ],
    })
    expect(ideaPrompt(i)).toBe('Dark mode\n\nFor the canvas.\n\nUsers asked twice.\n\n- Spec: https://x.dev')
  })

  it('picks a recording type', () => {
    expect(recorderMime((t) => t === 'audio/mp4')).toBe('audio/mp4')
    expect(recorderMime(() => false)).toBe('')
  })
})
