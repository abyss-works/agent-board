import { cva, type VariantProps } from 'class-variance-authority'
import type * as React from 'react'
import { cn } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex h-5 shrink-0 items-center gap-1 rounded-md border px-1.5 text-[0.6875rem] font-medium whitespace-nowrap [&_svg]:size-3',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-secondary text-secondary-foreground',
        outline: 'border-border text-muted-foreground',
        primary: 'border-transparent bg-primary/15 text-primary',
        destructive: 'border-transparent bg-destructive/15 text-destructive',
        success: 'border-transparent bg-emerald-500/15 text-emerald-400',
        warning: 'border-transparent bg-amber-500/15 text-amber-400',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

function Badge({ className, variant, ...props }: React.ComponentProps<'span'> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}

export { Badge }
