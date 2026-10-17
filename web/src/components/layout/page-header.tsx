import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function PageHeader({ title, meta, children, className }: { title: ReactNode; meta?: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <header className={cn('flex h-12 shrink-0 items-center gap-3 border-b px-4', className)}>
      <h2 className="text-[0.8125rem] font-semibold tracking-tight">{title}</h2>
      {meta ? <span className="text-xs text-muted-foreground">{meta}</span> : null}
      {children ? <div className="ml-auto flex items-center gap-2">{children}</div> : null}
    </header>
  )
}
