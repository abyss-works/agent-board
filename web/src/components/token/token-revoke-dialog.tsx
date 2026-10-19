import { toast } from 'sonner'
import { useRevokeActorToken } from '@/api/queries'
import type { ActorToken } from '@/api/types'
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
import { tokenLabel } from '@/lib/tokens'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  token: ActorToken | null
}

export function TokenRevokeDialog({ open, onOpenChange, token }: Props) {
  const revoke = useRevokeActorToken()
  if (!token) return null
  const label = tokenLabel(token)
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{label} 토큰을 폐기합니다.</AlertDialogTitle>
          <AlertDialogDescription>이 기기의 CLI는 다시 로그인해야 합니다.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={revoke.isPending}>취소</AlertDialogCancel>
          <AlertDialogAction
            disabled={revoke.isPending}
            onClick={(e) => {
              e.preventDefault()
              revoke.mutate(token.id, {
                onSuccess: () => {
                  toast.success(`${label} 토큰을 폐기했습니다`)
                  onOpenChange(false)
                },
                onError: (err) => toastError(err, '토큰을 폐기하지 못했습니다'),
              })
            }}
          >
            폐기
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
