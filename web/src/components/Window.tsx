import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** A Windows XP window: blue frame with a rounded Luna title bar, then the body. */
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
  /** Title bar buttons (right side), e.g. <TitleButton>. */
  controls?: ReactNode
  inactive?: boolean
  className?: string
  bodyClassName?: string
  children: ReactNode
}) {
  return (
    <section className={cn('win-window flex flex-col px-[3px] pb-[3px]', className)}>
      <div className={cn('-mx-[3px] flex h-[30px] shrink-0 items-center gap-1.5 pr-1 pl-2 text-[13px]', inactive ? 'win-titlebar-inactive' : 'win-titlebar')}>
        {icon}
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {controls}
      </div>
      <div className={cn('min-h-0 flex-1', bodyClassName)}>{children}</div>
    </section>
  )
}

/** A title bar button (minimise, maximise, close); `close` makes it the red X. */
export function TitleButton({ label, onClick, close, children }: { label: string; onClick?: () => void; close?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      data-close={close || undefined}
      className="win-caption flex size-[21px] items-center justify-center [&_svg]:size-3 [&_svg]:stroke-[3]"
    >
      {children}
    </button>
  )
}
