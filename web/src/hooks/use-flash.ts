import { useEffect, useRef, useState } from 'react'
import { changedKeys } from '@/lib/highlight'

const FLASH_MS = 2500

/**
 * 폴링으로 바뀐 항목을 잠깐 강조한다. items가 바뀔 때마다 이전 스냅샷과 비교해
 * 새로 생겼거나 version이 달라진 키를 FLASH_MS 동안 돌려준다. 첫 로드는 강조하지 않는다.
 * 목록이 통째로 바뀌는 곳(프로젝트·필터 전환)은 이 훅을 쓰는 컴포넌트에 key를 줘서 스냅샷을 비운다.
 */
export function useFlash<T, K>(items: T[] | undefined, key: (item: T) => K, version: (item: T) => string): Set<K> {
  const prev = useRef<Map<K, string> | null>(null)
  const timers = useRef<Set<number>>(new Set())
  const [flashed, setFlashed] = useState<Set<K>>(() => new Set())

  // 언마운트 때만 남은 타이머를 정리한다.
  useEffect(() => {
    const pending = timers.current
    return () => {
      for (const t of pending) window.clearTimeout(t)
    }
  }, [])

  useEffect(() => {
    if (!items) return
    const next = new Map(items.map((i) => [key(i), version(i)] as const))
    const changed = changedKeys(prev.current, next)
    prev.current = next
    if (changed.size === 0) return
    setFlashed((cur) => new Set([...cur, ...changed]))
    const t = window.setTimeout(() => {
      timers.current.delete(t)
      setFlashed((cur) => {
        const out = new Set(cur)
        for (const k of changed) out.delete(k)
        return out
      })
    }, FLASH_MS)
    timers.current.add(t)
    // key/version은 안정적인 선택자라고 가정하고 items가 바뀔 때만 비교한다.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items])

  return flashed
}
