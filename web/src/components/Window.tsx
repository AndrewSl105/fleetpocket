import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** A ruled panel: square ink frame, a mono title strip on a 2 px rule, then the body. */
export function Window({
  title,
  icon,
  controls,
  inactive,
  className,
  bodyClassName,
  children,
}: {
  title: ReactNode
  icon?: ReactNode
  /** Title strip buttons (right side), e.g. <TitleButton>. */
  controls?: ReactNode
  inactive?: boolean
  className?: string
  bodyClassName?: string
  children: ReactNode
}) {
  return (
    <section className={cn('fm-panel flex flex-col', className)}>
      <div className={cn('flex h-9 shrink-0 items-center gap-2 pr-1 pl-3', inactive ? 'fm-strip-muted' : 'fm-strip')}>
        {icon}
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {controls}
      </div>
      <div className={cn('min-h-0 flex-1', bodyClassName)}>{children}</div>
    </section>
  )
}

/** A strip button (minimise, maximise, close). */
export function TitleButton({ label, onClick, close, children }: { label: string; onClick?: () => void; close?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      data-close={close || undefined}
      className="fm-icon-btn flex size-7 items-center justify-center [&_svg]:size-3.5 [&_svg]:stroke-2"
    >
      {children}
    </button>
  )
}
