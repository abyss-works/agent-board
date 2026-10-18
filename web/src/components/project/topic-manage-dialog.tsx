import { PlusIcon, Trash2Icon } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { useCreateTopic, useDeleteTopic, usePatchTopic, useTopics } from '@/api/queries'
import type { Project, Topic } from '@/api/types'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { InlineInput } from '@/components/issue/inline-edit'
import { PropertySelect, type SelectOption } from '@/components/issue/property-select'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { canDeleteTopic, groupTopics, type TopicGroup } from '@/lib/topics'
import { toastError } from '@/lib/toast'
import { cn } from '@/lib/utils'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  project: Project | null
}

function AddTopicForm({ project, groups }: { project: Project; groups: TopicGroup[] }) {
  const create = useCreateTopic()
  const [name, setName] = useState('')
  // 빈 문자열은 새 대주제, 숫자는 그 대주제 아래 주제다.
  const [parent, setParent] = useState('')
  const parentExists = parent === '' || groups.some((g) => String(g.top.id) === parent)
  const options: SelectOption<string>[] = [
    { value: '', label: '새 대주제' },
    ...groups.map((g) => ({ value: String(g.top.id), label: `${g.top.name} 아래 주제` })),
  ]

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) return
    const parentId = parent !== '' && parentExists ? Number(parent) : null
    create.mutate(
      { project: project.key, body: parentId === null ? { name: trimmed } : { name: trimmed, parent: parentId } },
      {
        onSuccess: (tp) => {
          toast.success(`${tp.path}을(를) 추가했습니다`)
          setName('')
        },
        onError: (err) => toastError(err, '주제를 추가하지 못했습니다'),
      },
    )
  }

  return (
    <form onSubmit={submit} className="flex items-center gap-1.5">
      <PropertySelect value={parentExists ? parent : ''} options={options} onChange={setParent} title="추가할 위치" className="max-w-44 border-border" />
      <Input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder={parent === '' || !parentExists ? '대주제 이름' : '주제 이름'}
        className="h-7 flex-1 text-xs"
        maxLength={50}
        aria-label="새 주제 이름"
      />
      <Button type="submit" size="sm" disabled={!name.trim() || create.isPending}>
        <PlusIcon />
        추가
      </Button>
    </form>
  )
}

function TopicRow({ topic, depth, canDelete, onDelete }: { topic: Topic; depth: 0 | 1; canDelete: boolean; onDelete: (t: Topic) => void }) {
  const patch = usePatchTopic()
  return (
    <li className={cn('group flex items-center gap-1 rounded-md pr-1 hover:bg-accent/30', depth === 1 ? 'pl-6' : 'pl-1')}>
      <span className={cn('size-1.5 shrink-0 rounded-full', depth === 0 ? 'bg-sky-400' : 'bg-muted-foreground/50')} />
      <InlineInput
        value={topic.name}
        onCommit={(v) =>
          v &&
          patch.mutate({ id: topic.id, patch: { name: v } }, { onError: (err) => toastError(err, '이름을 바꾸지 못했습니다') })
        }
        className={cn('min-w-0 flex-1', depth === 0 && 'font-medium')}
      />
      <Button
        variant="ghost"
        size="icon-sm"
        className="opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
        disabled={!canDelete}
        onClick={() => onDelete(topic)}
        title={canDelete ? `${topic.path} 삭제` : '하위 주제를 먼저 삭제하세요'}
        aria-label={`${topic.path} 삭제`}
      >
        <Trash2Icon />
      </Button>
    </li>
  )
}

function TopicManager({ project }: { project: Project }) {
  const topics = useTopics(project.key)
  const del = useDeleteTopic()
  const [deleting, setDeleting] = useState<Topic | null>(null)
  const groups = groupTopics(topics.data?.items ?? [])

  return (
    <>
      <AddTopicForm project={project} groups={groups} />
      <div className="max-h-80 overflow-y-auto rounded-lg border p-1.5">
        {topics.isPending ? <Loading className="py-6" /> : null}
        {topics.isError ? <QueryError error={topics.error} onRetry={() => void topics.refetch()} className="m-1" /> : null}
        {topics.data && groups.length === 0 ? <EmptyState className="py-6">주제가 없습니다. 위에서 대주제를 추가하세요.</EmptyState> : null}
        <ul className="space-y-0.5">
          {groups.map((g) => (
            <li key={g.top.id}>
              <ul className="space-y-0.5">
                <TopicRow topic={g.top} depth={0} canDelete={canDeleteTopic(g)} onDelete={setDeleting} />
                {g.children.map((c) => (
                  <TopicRow key={c.id} topic={c} depth={1} canDelete onDelete={setDeleting} />
                ))}
              </ul>
            </li>
          ))}
        </ul>
      </div>

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{deleting ? `${deleting.path}을(를) 삭제할까요?` : ''}</AlertDialogTitle>
            <AlertDialogDescription>
              소프트 삭제입니다. 이 주제를 가진 이슈는 그대로 두고 이슈에는 주제 경로가 계속 보입니다. 새 이슈에는 붙일 수 없고, 삭제한 주제는 같은 이름으로 다시 만들 수 있습니다.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={del.isPending}>취소</AlertDialogCancel>
            <AlertDialogAction
              disabled={del.isPending}
              onClick={(e) => {
                e.preventDefault()
                if (!deleting) return
                del.mutate(deleting.id, {
                  onSuccess: () => {
                    toast.success(`${deleting.path}을(를) 삭제했습니다`)
                    setDeleting(null)
                  },
                  onError: (err) => toastError(err, '주제를 삭제하지 못했습니다'),
                })
              }}
            >
              삭제
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

/** 프로젝트의 대주제와 주제를 만들고 이름을 바꾸고 지우는 대화상자. 단계는 대주제와 주제 둘뿐이다. */
export function TopicManageDialog({ open, onOpenChange, project }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{project ? `${project.key} 주제 관리` : '주제 관리'}</DialogTitle>
          <DialogDescription>이슈를 묶는 대주제와 그 아래 주제입니다. 이름을 누르면 바꿀 수 있습니다.</DialogDescription>
        </DialogHeader>
        {project ? <TopicManager project={project} /> : null}
      </DialogContent>
    </Dialog>
  )
}
