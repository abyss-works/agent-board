import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { ApiError } from '@/api/client'
import { useCreateMilestone, usePatchMilestone } from '@/api/queries'
import type { Milestone } from '@/api/types'
import { FieldError } from '@/components/feedback'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  MILESTONE_DESCRIPTION_MAX,
  MILESTONE_NAME_MAX,
  formFromMilestone,
  milestoneCreateBody,
  milestonePatchBody,
  validateMilestoneForm,
  type MilestoneForm as FormValues,
} from '@/lib/milestones'
import { toastError } from '@/lib/toast'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 마일스톤을 만들거나 수정하는 프로젝트의 키 */
  projectKey: string
  /** 있으면 수정, 없으면 생성 */
  milestone?: Milestone
}

function MilestoneForm({ projectKey, milestone, onDone }: { projectKey: string; milestone?: Milestone; onDone: () => void }) {
  const [form, setForm] = useState<FormValues>(() => formFromMilestone(milestone))
  const [errors, setErrors] = useState<Record<string, string>>({})
  const create = useCreateMilestone()
  const patch = usePatchMilestone()
  const pending = create.isPending || patch.isPending

  const applyError = (err: unknown) => {
    if (err instanceof ApiError && (err.status === 422 || err.status === 400 || err.status === 409)) {
      const next: Record<string, string> = {}
      for (const d of err.details) {
        if (d.reason === 'duplicate') next.name = '같은 프로젝트에 같은 이름의 마일스톤이 있습니다.'
        else if (d.field) next[d.field] = d.message ?? d.reason ?? '잘못된 값입니다.'
      }
      if (Object.keys(next).length > 0) {
        setErrors(next)
        return
      }
    }
    toastError(err, milestone ? '마일스톤을 수정하지 못했습니다' : '마일스톤을 만들지 못했습니다')
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const local = validateMilestoneForm(form)
    if (Object.keys(local).length > 0) {
      setErrors(local)
      return
    }
    setErrors({})
    if (milestone) {
      const body = milestonePatchBody(milestone, form)
      if (Object.keys(body).length === 0) {
        onDone()
        return
      }
      patch.mutate(
        { id: milestone.id, patch: body },
        {
          onSuccess: (m) => {
            toast.success(`마일스톤 ${m.name}을(를) 수정했습니다`)
            onDone()
          },
          onError: applyError,
        },
      )
    } else {
      create.mutate(
        { project: projectKey, body: milestoneCreateBody(form) },
        {
          onSuccess: (m) => {
            toast.success(`마일스톤 ${m.name}을(를) 만들었습니다`)
            onDone()
          },
          onError: applyError,
        },
      )
    }
  }

  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>{milestone ? `마일스톤 ${milestone.name} 수정` : `${projectKey} 새 마일스톤`}</DialogTitle>
        <DialogDescription>
          {milestone ? '이름, 설명, 목표일을 바꿉니다. 닫기와 삭제는 목록에서 합니다.' : '출시나 목표 단위로 이슈를 묶습니다. 이슈는 마일스톤 하나에만 속합니다.'}
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-3">
        <div>
          <Label htmlFor="milestone-name">이름</Label>
          <Input
            id="milestone-name"
            value={form.name}
            onChange={(e) => setForm((cur) => ({ ...cur, name: e.target.value }))}
            placeholder="v0.2.0"
            aria-invalid={!!errors.name}
            className="mt-1"
            autoFocus
            maxLength={MILESTONE_NAME_MAX}
          />
          <FieldError message={errors.name} />
        </div>
        <div>
          <Label htmlFor="milestone-description">설명</Label>
          <Textarea
            id="milestone-description"
            value={form.description}
            onChange={(e) => setForm((cur) => ({ ...cur, description: e.target.value }))}
            placeholder={`선택, ${MILESTONE_DESCRIPTION_MAX}자까지`}
            aria-invalid={!!errors.description}
            className="mt-1 min-h-24"
          />
          <FieldError message={errors.description} />
        </div>
        <div>
          <div className="flex h-6 items-center justify-between">
            <Label htmlFor="milestone-target-date">목표일</Label>
            {form.targetDate ? (
              <Button type="button" variant="ghost" size="xs" onClick={() => setForm((cur) => ({ ...cur, targetDate: '' }))}>
                지우기
              </Button>
            ) : null}
          </div>
          <Input
            id="milestone-target-date"
            type="date"
            value={form.targetDate}
            onChange={(e) => setForm((cur) => ({ ...cur, targetDate: e.target.value }))}
            aria-invalid={!!errors.target_date}
            className="mt-1"
          />
          <p className="mt-1 text-[0.6875rem] text-muted-foreground">비워 두면 목표일이 없습니다.</p>
          <FieldError message={errors.target_date} />
        </div>
      </div>
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone} disabled={pending}>
          취소
        </Button>
        <Button type="submit" disabled={pending}>
          {milestone ? '저장' : '만들기'}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** 마일스톤을 만들거나 이름·설명·목표일을 고치는 대화상자 */
export function MilestoneDialog({ open, onOpenChange, projectKey, milestone }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        {/* Dialog 내용은 닫히면 언마운트되므로 열 때마다 폼 상태가 새로 시작한다. */}
        <MilestoneForm projectKey={projectKey} milestone={milestone} onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}
