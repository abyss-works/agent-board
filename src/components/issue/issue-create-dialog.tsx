import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { ApiError } from '@/api/client'
import { useCreateIssue, useProjects } from '@/api/queries'
import type { IssueCreate, IssueStatus, IssueType, Priority, Project } from '@/api/types'
import { useAppState } from '@/app/state'
import { FieldError } from '@/components/feedback'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { parseLabels } from '@/lib/parse'
import { toastError } from '@/lib/toast'
import { PRIORITY_OPTIONS, STATUS_OPTIONS, TYPE_OPTIONS } from './options'
import { PropertySelect, type SelectOption } from './property-select'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 처음 선택할 상태(보드 열에서 열 때) */
  initialStatus?: IssueStatus
}

function CreateForm({ onDone, initialStatus, projects, projectsLoaded }: { onDone: () => void; initialStatus?: IssueStatus; projects: Project[]; projectsLoaded: boolean }) {
  const { project: selectedProject } = useAppState()
  const navigate = useNavigate()
  const create = useCreateIssue()

  const [project, setProject] = useState('')
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [type, setType] = useState<IssueType>('feature')
  const [status, setStatus] = useState<IssueStatus>(initialStatus ?? 'backlog')
  const [priority, setPriority] = useState<Priority>('P2')
  const [labels, setLabels] = useState('')
  const [recheck, setRecheck] = useState('')
  const [errors, setErrors] = useState<Record<string, string>>({})

  const activeProjects = projects.filter((p) => p.status === 'active')
  const projectOptions: SelectOption<string>[] = activeProjects.map((p) => ({ value: p.key, label: `${p.key} · ${p.name}` }))
  // 고른 적이 없으면 사이드바에서 선택한 활성 프로젝트, 없으면 첫 활성 프로젝트를 쓴다.
  const effectiveProject =
    project && activeProjects.some((p) => p.key === project)
      ? project
      : (activeProjects.find((p) => p.key === selectedProject)?.key ?? activeProjects[0]?.key ?? '')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const local: Record<string, string> = {}
    if (!effectiveProject) local.project = '프로젝트를 고르세요.'
    if (title.trim().length < 1 || title.trim().length > 200) local.title = '1~200자여야 합니다.'
    if (type === 'observation' && !recheck.trim()) local.recheck_condition = '관측에는 재검토 조건이 필요합니다.'
    if (Object.keys(local).length > 0) {
      setErrors(local)
      return
    }
    setErrors({})
    const bodyText = body.trim()
    const labelList = parseLabels(labels)
    const payload: IssueCreate = {
      project: effectiveProject,
      title: title.trim(),
      type,
      status,
      priority,
      ...(bodyText ? { body: bodyText } : {}),
      ...(labelList.length ? { labels: labelList } : {}),
      ...(type === 'observation' ? { recheck_condition: recheck.trim() } : {}),
    }
    create.mutate(payload, {
      onSuccess: (issue) => {
        toast.success(`${issue.key}를 만들었습니다`)
        onDone()
        navigate(`/issues/${issue.key}`)
      },
      onError: (err) => {
        if (err instanceof ApiError && (err.status === 422 || err.status === 400) && err.details.length > 0) {
          const next: Record<string, string> = {}
          for (const d of err.details) {
            if (!d.field) continue
            const field = d.field.replace(/\[\d+\]$/, '')
            next[field] = d.message ?? d.reason ?? '잘못된 값입니다.'
          }
          setErrors(next)
          if (Object.keys(next).length > 0) return
        }
        toastError(err, '이슈를 만들지 못했습니다')
      },
    })
  }

  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>새 이슈</DialogTitle>
        <DialogDescription>행위자 owner로 등록합니다.</DialogDescription>
      </DialogHeader>

      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-1.5">
          <PropertySelect
            value={effectiveProject}
            options={projectOptions}
            onChange={setProject}
            title="프로젝트"
            className="border-border font-mono text-xs"
            disabled={projectOptions.length === 0}
          />
          <PropertySelect value={type} options={TYPE_OPTIONS} onChange={setType} title="종류" className="border-border" />
          <PropertySelect value={status} options={STATUS_OPTIONS} onChange={setStatus} title="상태" className="border-border" />
          <PropertySelect value={priority} options={PRIORITY_OPTIONS} onChange={setPriority} title="우선순위" className="border-border" />
        </div>
        <FieldError message={errors.project ?? errors.type ?? errors.status ?? errors.priority} />
        {projectOptions.length === 0 && projectsLoaded ? (
          <p className="text-[11px] text-destructive">활성 프로젝트가 없습니다. 먼저 프로젝트를 만드세요.</p>
        ) : null}

        <div>
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="제목"
            aria-invalid={!!errors.title}
            className="h-9 border-transparent px-1 text-[15px] font-medium shadow-none focus-visible:border-transparent focus-visible:ring-0"
            autoFocus
            maxLength={200}
          />
          <FieldError message={errors.title} />
        </div>
        <div>
          <Textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="본문(마크다운)"
            aria-invalid={!!errors.body}
            className="min-h-40 border-transparent px-1 font-mono text-[12.5px] focus-visible:border-transparent focus-visible:ring-0"
          />
          <FieldError message={errors.body} />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label htmlFor="issue-labels">라벨</Label>
            <Input
              id="issue-labels"
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
              placeholder="api cli (공백·쉼표 구분)"
              aria-invalid={!!errors.labels}
              className="mt-1 font-mono text-xs"
              spellCheck={false}
            />
            <FieldError message={errors.labels} />
          </div>
          {type === 'observation' ? (
            <div>
              <Label htmlFor="issue-recheck">재검토 조건</Label>
              <Input
                id="issue-recheck"
                value={recheck}
                onChange={(e) => setRecheck(e.target.value)}
                placeholder="언제 다시 볼지"
                aria-invalid={!!errors.recheck_condition}
                className="mt-1"
                maxLength={500}
              />
              <FieldError message={errors.recheck_condition} />
            </div>
          ) : null}
        </div>
      </div>

      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone} disabled={create.isPending}>
          취소
        </Button>
        <Button type="submit" disabled={create.isPending || !effectiveProject}>
          이슈 만들기
        </Button>
      </DialogFooter>
    </form>
  )
}

export function IssueCreateDialog({ open, onOpenChange, initialStatus }: Props) {
  const projects = useProjects()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        {/* Dialog 내용은 닫히면 언마운트되므로 열 때마다 폼 상태가 새로 시작한다. */}
        <CreateForm onDone={() => onOpenChange(false)} initialStatus={initialStatus} projects={projects.data?.items ?? []} projectsLoaded={projects.isSuccess} />
      </DialogContent>
    </Dialog>
  )
}
