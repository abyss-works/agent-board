import { toast } from 'sonner'
import { useDeleteProject } from '@/api/queries'
import type { Project } from '@/api/types'
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
import { toastError } from '@/lib/toast'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  project: Project | null
  onDeleted?: (project: Project) => void
}

export function ProjectDeleteDialog({ open, onOpenChange, project, onDeleted }: Props) {
  const del = useDeleteProject()
  if (!project) return null
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>프로젝트 {project.key}를 삭제할까요?</AlertDialogTitle>
          <AlertDialogDescription>
            소프트 삭제입니다. 상태가 '삭제'로 바뀌고 이슈와 활동 기록은 남습니다. 삭제된 프로젝트의 이슈에는 쓸 수 없습니다. 되살리려면
            프로젝트 설정에서 상태를 '활성'으로 바꿉니다.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={del.isPending}>취소</AlertDialogCancel>
          <AlertDialogAction
            disabled={del.isPending}
            onClick={(e) => {
              e.preventDefault()
              del.mutate(project.key, {
                onSuccess: (p) => {
                  toast.success(`프로젝트 ${p.key}를 삭제했습니다`)
                  onOpenChange(false)
                  onDeleted?.(p)
                },
                onError: (err) => toastError(err, '프로젝트를 삭제하지 못했습니다'),
              })
            }}
          >
            삭제
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
