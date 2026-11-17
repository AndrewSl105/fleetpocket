import { costLabel, usd } from '@/lib/format'

/** A cost in the one format (usd()), with its meaning spelled out for screen readers. */
export function Cost({ value, estimated, unknown, className }: { value: number; estimated?: boolean; unknown?: boolean; className?: string }) {
  const text = usd(value, estimated, unknown)
  const label = costLabel(value, estimated, unknown)
  if (label === text) return <span className={className}>{text}</span>
  return (
    <span className={className}>
      <span aria-hidden="true">{text}</span>
      <span className="sr-only">{label}</span>
    </span>
  )
}
