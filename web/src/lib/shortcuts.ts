/** 사이드바 주요 메뉴 단축키. 이 네 개 말고 다른 단축키는 두지 않는다. */
export const MENU_SHORTCUTS: ReadonlyArray<{ key: string; path: string; label: string }> = [
  { key: '1', path: '/overview', label: '현황' },
  { key: '2', path: '/board', label: '보드' },
  { key: '3', path: '/issues', label: '이슈' },
  { key: '4', path: '/activity', label: '활동' },
]

export interface EditableLike {
  tagName?: string
  isContentEditable?: boolean
}

export interface KeyEventLike {
  key: string
  ctrlKey: boolean
  altKey: boolean
  metaKey: boolean
  target: EditableLike | EventTarget | null
}

export function isEditableTarget(target: unknown): boolean {
  if (!target || typeof target !== 'object') return false
  const el = target as EditableLike
  const tag = el.tagName?.toUpperCase()
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return el.isContentEditable === true
}

/** 눌린 키가 메뉴 단축키면 이동할 경로를, 아니면 null을 돌려준다. 이슈 오버레이가 열려 있으면 뒤 화면이 바뀌지 않도록 null이다. */
export function shortcutRoute(e: KeyEventLike, overlayOpen = false): string | null {
  if (overlayOpen) return null
  if (e.ctrlKey || e.altKey || e.metaKey) return null
  if (isEditableTarget(e.target)) return null
  return MENU_SHORTCUTS.find((s) => s.key === e.key)?.path ?? null
}
