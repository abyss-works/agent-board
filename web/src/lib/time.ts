const KST = 'Asia/Seoul'

const dateTime = new Intl.DateTimeFormat('sv-SE', {
  timeZone: KST,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

function parse(iso: string): Date | null {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d
}

/** `2026-10-17 14:46:06` (KST) */
export function formatKst(iso: string): string {
  const d = parse(iso)
  if (!d) return iso
  // sv-SE 로캘은 `2026-10-17 14:46:06` 꼴을 돌려준다.
  return dateTime.format(d).replace('T', ' ')
}

/** `2026-10-17` (KST) */
export function formatKstDate(iso: string): string {
  return formatKst(iso).slice(0, 10)
}

/** `14:46:06` (KST) */
export function formatKstTime(iso: string): string {
  return formatKst(iso).slice(11, 19)
}

/** 상대 시각. 1분 안은 "방금", 7일을 넘으면 KST 날짜. */
export function formatRelative(iso: string, now: Date = new Date()): string {
  const d = parse(iso)
  if (!d) return iso
  const sec = Math.floor((now.getTime() - d.getTime()) / 1000)
  if (sec < 60) return '방금'
  if (sec < 3600) return `${Math.floor(sec / 60)}분 전`
  if (sec < 86400) return `${Math.floor(sec / 3600)}시간 전`
  if (sec < 86400 * 7) return `${Math.floor(sec / 86400)}일 전`
  return formatKstDate(iso)
}

/** 만료까지 남은 시간. `9분 30초`, 딱 떨어지는 분은 `10분`, 1분 미만은 `45초`, 시각이 지났으면 `만료됨`. */
export function formatRemaining(iso: string, now: Date = new Date()): string {
  const d = parse(iso)
  if (!d) return iso
  const sec = Math.ceil((d.getTime() - now.getTime()) / 1000)
  if (sec <= 0) return '만료됨'
  const min = Math.floor(sec / 60)
  const rest = sec % 60
  if (min === 0) return `${rest}초`
  return rest === 0 ? `${min}분` : `${min}분 ${rest}초`
}
