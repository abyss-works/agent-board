const CHIP_COLORS = [
  'bg-sky-400',
  'bg-emerald-400',
  'bg-violet-400',
  'bg-amber-400',
  'bg-rose-400',
  'bg-teal-400',
  'bg-fuchsia-400',
  'bg-lime-400',
]

/** 라벨·행위자 이름에서 늘 같은 색을 고른다. */
export function hashColor(text: string): string {
  let hash = 0
  for (const ch of text) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0
  return CHIP_COLORS[hash % CHIP_COLORS.length]
}
