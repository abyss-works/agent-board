import { toast } from 'sonner'
import { ApiError, errorMessage, isPreconditionFailed } from '@/api/client'

/** 쓰기 실패를 알린다. 412는 "다른 곳에서 바뀜"으로, 400/422는 details를 함께 보여 준다. */
export function toastError(err: unknown, fallback = '요청이 실패했습니다') {
  if (isPreconditionFailed(err)) {
    toast.warning('다른 곳에서 먼저 바뀌었습니다', { description: '최신 내용을 다시 불러왔습니다. 확인한 뒤 다시 시도해 주세요.' })
    return
  }
  if (err instanceof ApiError && err.details.length > 0) {
    toast.error(err.message || fallback, {
      description: err.details.map((d) => (d.field ? `${d.field}: ${d.message ?? d.reason ?? ''}` : (d.message ?? d.reason ?? ''))).join('\n'),
    })
    return
  }
  toast.error(fallback, { description: errorMessage(err) })
}
