// Pure helpers of the Ideas board (unit-tested in ideas.test.ts).
import type { Idea, IdeaColumn } from '@/api/types'

/** 4200 → "0:04". */
export function formatDuration(ms: number | null | undefined): string {
  const s = Math.max(0, Math.round((ms ?? 0) / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/** Cards per column, in board order (the server sorts by position). Unknown columns are dropped. */
export function byColumn(ideas: Idea[], columns: IdeaColumn[]): Map<string, Idea[]> {
  const out = new Map<string, Idea[]>(columns.map((c) => [c.id, []]))
  for (const i of ideas) out.get(i.column)?.push(i)
  return out
}

/** The card a dropped card goes after: the one before `index` in the target column (without the card itself); null = on top. */
export function afterIdAt(cards: Idea[], movingId: number, index: number): number | null {
  const rest = cards.filter((c) => c.id !== movingId)
  if (index <= 0) return null
  return rest[Math.min(index, rest.length) - 1]?.id ?? null
}

/** Every tag on the board, sorted. */
export function tagsOf(ideas: Idea[]): string[] {
  return [...new Set(ideas.flatMap((i) => i.tags))].sort()
}

/** "ui, Perf ,, #ux" → ["ui", "perf", "ux"]. */
export function parseTags(s: string): string[] {
  return [...new Set(s.split(',').map((t) => t.trim().replace(/^#/, '').toLowerCase()).filter(Boolean))]
}

/** The text a task started from the idea gets: title, text, transcripts not already in the text, links. */
export function ideaPrompt(idea: Idea): string {
  const parts = [idea.title.trim()]
  const body = idea.body.trim()
  if (body && body !== idea.title.trim()) parts.push(body)
  for (const v of idea.voice_notes) {
    const tr = v.transcript?.trim()
    if (tr && !body.includes(tr)) parts.push(tr)
  }
  const links = idea.attachments.filter((a) => a.kind === 'link' && a.url)
  if (links.length) parts.push(links.map((l) => `- ${l.title ? `${l.title}: ` : ''}${l.url}`).join('\n'))
  return parts.join('\n\n')
}

/** URLs pasted into the quick-add field become links; the rest stays text. */
export function splitLinks(text: string): { text: string; links: string[] } {
  const links: string[] = []
  const rest = text.replace(/https?:\/\/[^\s<>"']+/g, (u) => {
    links.push(u)
    return ''
  })
  return { text: rest.replace(/[ \t]+/g, ' ').trim(), links }
}

/** The first recording type this browser supports ('' = the browser's default). */
export function recorderMime(isSupported: (t: string) => boolean): string {
  return ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4', 'audio/ogg;codecs=opus'].find((t) => isSupported(t)) ?? ''
}
