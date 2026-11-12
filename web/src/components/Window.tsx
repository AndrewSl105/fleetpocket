import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** A Windows 98 window: bevelled frame, gradient title bar, then the body. */
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
    <section className={cn('win-window flex flex-col p-[3px]', className)}>
      <div className={cn('flex h-[22px] shrink-0 items-center gap-1 pr-0.5 pl-1 text-sm', inactive ? 'win-titlebar-inactive' : 'win-titlebar')}>
        {icon}
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {controls}
      </div>
      <div className={cn('min-h-0 flex-1', bodyClassName)}>{children}</div>
    </section>
  )
}

/** A small raised title bar button (minimise, maximise, close). */
export function TitleButton({ label, onClick, children }: { label: string; onClick?: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="win-raised flex h-[16px] w-[18px] items-center justify-center text-foreground active:win-pressed [&_svg]:size-3"
    >
      {children}
    </button>
  )
}
