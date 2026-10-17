import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'

interface InlineProps {
  value: string
  onCommit: (value: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
  /** 표시 모드에서 값을 그리는 방법. 없으면 값을 그대로 보여 준다. */
  render?: (value: string) => ReactNode
  mono?: boolean
}

/**
 * 클릭하면 입력 칸이 되는 한 줄 값. Enter·포커스 이탈로 저장하고 Esc로 취소한다.
 * 값이 바뀌지 않았으면 onCommit을 부르지 않는다.
 */
export function InlineInput({ value, onCommit, placeholder, disabled, className, render, mono }: InlineProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const ref = useRef<HTMLInputElement>(null)

  const startEditing = () => {
    setDraft(value)
    setEditing(true)
  }

  const commit = () => {
    setEditing(false)
    if (draft.trim() !== value.trim()) onCommit(draft.trim())
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setDraft(value)
      setEditing(false)
    }
  }

  if (editing) {
    return (
      <Input
        ref={ref}
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={onKeyDown}
        placeholder={placeholder}
        className={cn('h-7 text-xs', mono && 'font-mono', className)}
        spellCheck={false}
      />
    )
  }
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={startEditing}
      className={cn(
        'flex min-h-7 w-full cursor-text items-center rounded-md border border-transparent px-1.5 text-left text-xs transition-colors hover:border-border hover:bg-accent/40 disabled:cursor-default disabled:hover:border-transparent disabled:hover:bg-transparent',
        !value && 'text-muted-foreground/70',
        mono && 'font-mono',
        className,
      )}
    >
      {value ? (render ? render(value) : value) : (placeholder ?? '없음')}
    </button>
  )
}

interface AreaProps {
  value: string
  onCommit: (value: string) => void
  onCancel?: () => void
  placeholder?: string
  className?: string
  saving?: boolean
}

/** 본문·댓글 편집용 텍스트 영역. 저장·취소 버튼은 호출자가 그린다. */
export function MarkdownArea({ value, onCommit, onCancel, placeholder, className, saving }: AreaProps) {
  const [draft, setDraft] = useState(value)
  return (
    <div className={cn('space-y-2', className)}>
      <Textarea
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder={placeholder}
        className="min-h-48 font-mono text-[0.78125rem]"
        disabled={saving}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && onCancel) {
            e.preventDefault()
            onCancel()
          }
        }}
      />
      <div className="flex items-center justify-end gap-2">
        {onCancel ? (
          <button type="button" onClick={onCancel} disabled={saving} className="cursor-pointer rounded-md px-2.5 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground">
            취소
          </button>
        ) : null}
        <button
          type="button"
          onClick={() => onCommit(draft)}
          disabled={saving}
          className="cursor-pointer rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          저장
        </button>
      </div>
    </div>
  )
}
