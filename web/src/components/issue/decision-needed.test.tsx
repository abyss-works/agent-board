import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DecisionNeeded } from './decision-needed'

function render(value: string | null): string {
  return renderToStaticMarkup(<DecisionNeeded value={value} onCommit={() => {}} />)
}

describe('DecisionNeeded', () => {
  it('질문이 있으면 결정 필요 이름표와 질문을 보인다', () => {
    const html = render('캐시를 Redis로 할지 정해 주세요')
    expect(html).toContain('결정 필요')
    expect(html).toContain('캐시를 Redis로 할지 정해 주세요')
    expect(html).not.toContain('없음')
  })
  it('질문이 없으면 이름표와 비어 있음을 보이고 질문은 보이지 않는다', () => {
    const html = render(null)
    expect(html).toContain('결정 필요')
    expect(html).toContain('없음')
  })
  it('질문이 있든 없든 눌러서 고칠 수 있다', () => {
    expect(render('질문')).toContain('<button')
    expect(render(null)).toContain('<button')
  })
})
