import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import { shortcutRoute } from '@/lib/shortcuts'

/** 1~4 키로 사이드바 메뉴를 옮긴다. 입력 칸이나 수정 키가 눌린 상태, 이슈 오버레이가 열려 있는 동안에는 동작하지 않는다. */
export function useMenuShortcuts(overlayOpen: boolean) {
  const navigate = useNavigate()
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return
      const path = shortcutRoute(e, overlayOpen)
      if (!path) return
      e.preventDefault()
      navigate(path)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [navigate, overlayOpen])
}
