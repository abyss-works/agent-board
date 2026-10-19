import { useEffect, useState } from 'react'

/** 지정한 간격(ms)마다 현재 시각(epoch ms)을 새로 돌려줘 남은 시간 같은 표시를 갱신한다. */
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(id)
  }, [intervalMs])
  return now
}
