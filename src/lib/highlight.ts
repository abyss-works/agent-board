/**
 * 이전 스냅샷(키 → 버전 문자열)과 비교해 새로 생겼거나 버전이 바뀐 키를 돌려준다.
 * 이전 스냅샷이 없으면(첫 로드) 아무것도 강조하지 않는다.
 */
export function changedKeys<K>(prev: Map<K, string> | null, next: Map<K, string>): Set<K> {
  const out = new Set<K>()
  if (!prev) return out
  for (const [key, version] of next) {
    if (prev.get(key) !== version) out.add(key)
  }
  return out
}
