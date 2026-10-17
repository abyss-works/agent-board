import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { safeUrl } from '@/lib/sanitize'
import { Markdown } from './markdown'

function render(source: string): string {
  return renderToStaticMarkup(<Markdown source={source} />)
}

describe('safeUrl', () => {
  it('http, https, mailto, 상대 경로는 허용한다', () => {
    expect(safeUrl('https://example.com/a?b=1')).toBe('https://example.com/a?b=1')
    expect(safeUrl('http://127.0.0.1:8420/v1/health')).toBe('http://127.0.0.1:8420/v1/health')
    expect(safeUrl('mailto:a@b.c')).toBe('mailto:a@b.c')
    expect(safeUrl('/issues/AGB-1')).toBe('/issues/AGB-1')
    expect(safeUrl('#section')).toBe('#section')
  })
  it('javascript:, data:, vbscript:는 비운다', () => {
    expect(safeUrl('javascript:alert(1)')).toBe('')
    expect(safeUrl('JavaScript:alert(1)')).toBe('')
    expect(safeUrl(' javascript:alert(1)')).toBe('')
    expect(safeUrl('data:text/html;base64,PHNjcmlwdD4=')).toBe('')
    expect(safeUrl('vbscript:msgbox')).toBe('')
  })
})

describe('Markdown', () => {
  it('제목, 목록, 코드, 표(GFM)를 그린다', () => {
    const html = render('## 배경\n\n- 하나\n- 둘\n\n`code`\n\n| a | b |\n|---|---|\n| 1 | 2 |')
    expect(html).toContain('<h2>배경</h2>')
    expect(html).toContain('<li>하나</li>')
    expect(html).toContain('<code>code</code>')
    expect(html).toContain('<table>')
  })
  it('원시 HTML은 실행하지 않고 글자로 보여 준다', () => {
    const html = render('본문 <script>alert(1)</script> 끝')
    expect(html).not.toContain('<script>')
    expect(html).toContain('&lt;script&gt;')
  })
  it('이벤트 속성이 든 태그도 글자로 보여 준다', () => {
    const html = render('<img src=x onerror="alert(1)">')
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;img src=x onerror=&quot;alert(1)&quot;&gt;')
  })
  it('javascript: 링크는 href를 없앤다', () => {
    const html = render('[클릭](javascript:alert(1))')
    expect(html).not.toContain('javascript:')
    expect(html).toContain('클릭')
  })
  it('외부 링크는 새 창에서 열고 noopener noreferrer를 붙인다', () => {
    const html = render('[문서](https://example.com/doc)')
    expect(html).toContain('href="https://example.com/doc"')
    expect(html).toContain('target="_blank"')
    expect(html).toContain('rel="noopener noreferrer"')
  })
  it('이미지의 javascript: 주소는 비운다', () => {
    const html = render('![x](javascript:alert(1))')
    expect(html).not.toContain('javascript:')
  })
})
