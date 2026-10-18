import { useState, type FormEvent } from 'react'
import { FieldError } from '@/components/feedback'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { reasonError } from '@/lib/status'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  submitLabel: string
  /** 삭제처럼 되돌리기 어려운 동작이면 확인 버튼을 경고색으로 그린다. */
  destructive?: boolean
  pending?: boolean
  /** 앞뒤 공백을 뗀 사유 */
  onSubmit: (reason: string) => void
}

function ReasonForm({ title, description, submitLabel, destructive, pending, onSubmit, onCancel }: Omit<Props, 'open' | 'onOpenChange'> & { onCancel: () => void }) {
  const [text, setText] = useState('')
  const [touched, setTouched] = useState(false)
  const error = touched ? reasonError(text) : null

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    setTouched(true)
    if (reasonError(text)) return
    onSubmit(text.trim())
  }

  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
      </DialogHeader>
      <div>
        <Textarea
          autoFocus
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) submit()
          }}
          placeholder="사유 (활동 기록에 남습니다)"
          aria-invalid={!!error}
          disabled={pending}
          className="min-h-24"
        />
        <FieldError message={error} />
      </div>
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onCancel} disabled={pending}>
          취소
        </Button>
        <Button type="submit" variant={destructive ? 'destructive' : 'default'} disabled={pending}>
          {submitLabel}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** 상태 변경의 사유를 받는 대화상자. 취소와 삭제로 바꿀 때 쓰며, 사유 없이는 확인할 수 없다. */
export function ReasonDialog({ open, onOpenChange, ...rest }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        {/* Dialog 내용은 닫히면 언마운트되므로 열 때마다 입력이 비어 있다. */}
        <ReasonForm {...rest} onCancel={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}
