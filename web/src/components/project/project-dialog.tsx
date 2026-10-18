import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { ApiError } from '@/api/client'
import { useCreateProject, usePatchProject } from '@/api/queries'
import { PROJECT_STATUSES, type Project, type ProjectStatus } from '@/api/types'
import { FieldError } from '@/components/feedback'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PROJECT_STATUS_LABEL } from '@/lib/labels'
import { toastError } from '@/lib/toast'
import { cn } from '@/lib/utils'

const KEY_PATTERN = /^[A-Z][A-Z0-9]{1,9}$/

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 있으면 수정, 없으면 생성 */
  project?: Project
  onCreated?: (project: Project) => void
}

function ProjectForm({ project, onDone, onCreated }: { project?: Project; onDone: () => void; onCreated?: (p: Project) => void }) {
  const [key, setKey] = useState(project?.key ?? '')
  const [name, setName] = useState(project?.name ?? '')
  const [status, setStatus] = useState<ProjectStatus>(project?.status ?? 'active')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const create = useCreateProject()
  const patch = usePatchProject()
  const pending = create.isPending || patch.isPending

  const applyError = (err: unknown) => {
    if (err instanceof ApiError && (err.status === 422 || err.status === 400 || err.status === 409)) {
      const next: Record<string, string> = {}
      for (const d of err.details) {
        if (d.reason === 'duplicate_key') next.key = '이미 있는 프로젝트 키입니다.'
        else if (d.field) next[d.field] = d.message ?? d.reason ?? '잘못된 값입니다.'
      }
      if (Object.keys(next).length > 0) {
        setErrors(next)
        return
      }
    }
    toastError(err, project ? '프로젝트를 수정하지 못했습니다' : '프로젝트를 만들지 못했습니다')
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const local: Record<string, string> = {}
    if (!project && !KEY_PATTERN.test(key)) local.key = '대문자로 시작하는 2~10자의 대문자·숫자여야 합니다. 예: AGB'
    if (name.trim().length < 1 || name.trim().length > 100) local.name = '1~100자여야 합니다.'
    if (Object.keys(local).length > 0) {
      setErrors(local)
      return
    }
    setErrors({})
    if (project) {
      const body: { name?: string; status?: ProjectStatus } = {}
      if (name.trim() !== project.name) body.name = name.trim()
      if (status !== project.status) body.status = status
      if (Object.keys(body).length === 0) {
        onDone()
        return
      }
      patch.mutate(
        { key: project.key, patch: body },
        {
          onSuccess: () => {
            toast.success(`프로젝트 ${project.key}를 수정했습니다`)
            onDone()
          },
          onError: applyError,
        },
      )
    } else {
      create.mutate(
        { key, name: name.trim() },
        {
          onSuccess: (p) => {
            toast.success(`프로젝트 ${p.key}를 만들었습니다`)
            onDone()
            onCreated?.(p)
          },
          onError: applyError,
        },
      )
    }
  }

  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>{project ? `프로젝트 ${project.key} 설정` : '새 프로젝트'}</DialogTitle>
        <DialogDescription>
          {project ? '이름과 상태를 바꿉니다. 키는 바꿀 수 없습니다.' : '키는 이슈 키의 접두어가 되며 만든 뒤 바꿀 수 없습니다.'}
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-3">
        <div>
          <Label htmlFor="project-key">키</Label>
          <Input
            id="project-key"
            value={key}
            onChange={(e) => setKey(e.target.value.toUpperCase())}
            placeholder="AGB"
            disabled={!!project}
            aria-invalid={!!errors.key}
            className="mt-1 font-mono uppercase"
            autoFocus={!project}
            maxLength={10}
            spellCheck={false}
          />
          <FieldError message={errors.key} />
        </div>
        <div>
          <Label htmlFor="project-name">이름</Label>
          <Input
            id="project-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="agent-board"
            aria-invalid={!!errors.name}
            className="mt-1"
            autoFocus={!!project}
            maxLength={100}
          />
          <FieldError message={errors.name} />
        </div>
        {project ? (
          <div>
            <Label>상태</Label>
            <div className="mt-1 flex gap-1 rounded-md bg-muted p-0.5">
              {PROJECT_STATUSES.map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setStatus(s)}
                  className={cn(
                    'flex-1 cursor-pointer rounded px-2 py-1 text-xs transition-colors',
                    status === s ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {PROJECT_STATUS_LABEL[s]}
                </button>
              ))}
            </div>
            <p className="mt-1 text-[0.6875rem] text-muted-foreground">보관·삭제 상태의 프로젝트에는 이슈를 쓸 수 없습니다(409).</p>
            <FieldError message={errors.status} />
          </div>
        ) : null}
      </div>
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone} disabled={pending}>
          취소
        </Button>
        <Button type="submit" disabled={pending}>
          {project ? '저장' : '만들기'}
        </Button>
      </DialogFooter>
    </form>
  )
}

export function ProjectDialog({ open, onOpenChange, project, onCreated }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        {/* Dialog 내용은 닫히면 언마운트되므로 열 때마다 폼 상태가 새로 시작한다. */}
        <ProjectForm project={project} onDone={() => onOpenChange(false)} onCreated={onCreated} />
      </DialogContent>
    </Dialog>
  )
}
