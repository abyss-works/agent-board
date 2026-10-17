import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import { shortcutRoute } from '@/lib/shortcuts'

/** 1~4 키로 사이드바 메뉴를 옮긴다. 입력 칸이나 수정 키가 눌린 상태에서는 동작하지 않는다. */
export function useMenuShortcuts() {
  const navigate = useNavigate()
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return
      const path = shortcutRoute(e)
      if (!path) return
      e.preventDefault()
      navigate(path)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [navigate])
}
