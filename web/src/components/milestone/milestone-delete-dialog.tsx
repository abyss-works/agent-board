import { toast } from 'sonner'
import { useDeleteMilestone } from '@/api/queries'
import type { Milestone } from '@/api/types'
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
  milestone: Milestone | null
}

export function MilestoneDeleteDialog({ open, onOpenChange, milestone }: Props) {
  const del = useDeleteMilestone()
  if (!milestone) return null
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>마일스톤 {milestone.name}을(를) 삭제할까요?</AlertDialogTitle>
          <AlertDialogDescription>
            소프트 삭제입니다. 상태가 '삭제됨'으로 바뀌고 이 마일스톤의 이슈는 그대로 남아 이슈에는 마일스톤 이름이 계속 보입니다. 새 이슈에는 붙일 수 없고, 같은 이름으로 다시 만들 수 있습니다.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={del.isPending}>취소</AlertDialogCancel>
          <AlertDialogAction
            disabled={del.isPending}
            onClick={(e) => {
              e.preventDefault()
              del.mutate(milestone.id, {
                onSuccess: (m) => {
                  toast.success(`마일스톤 ${m.name}을(를) 삭제했습니다`)
                  onOpenChange(false)
                },
                onError: (err) => toastError(err, '마일스톤을 삭제하지 못했습니다'),
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
