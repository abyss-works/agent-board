import { describe, expect, it } from 'vitest'
import { composeBody, hasContent, parseBody, templateFields, toMarkdown } from './template-form'

const FEATURE = `## 배경
왜 필요한지. 요청한 사람과 날짜, 관련 결정이나 이슈.

## 할 일
- 작업 단위 하나씩

## 완료 조건
- 확인할 수 있는 조건. 실행할 명령, 볼 화면, 받을 응답.
`

const DECISION = `## 현황
결정이 필요한 이유와 지금 상태.

## 결정할 것
결정 하나를 한 문장으로.

## 선택지
1. 선택지: 무엇을 뜻하는지와 고른 뒤의 결과
2. 선택지: 무엇을 뜻하는지와 고른 뒤의 결과
- 추천: 선택지 번호와 근거

## 영향
결정을 기다리는 작업(이슈 키)과 결정이 늦을 때 생기는 일.
`

describe('templateFields', () => {
  it('"## " 제목마다 칸을 만들고 그 아래 안내 문장을 힌트로 담는다', () => {
    expect(templateFields({ body: FEATURE })).toEqual([
      { heading: '배경', hint: '왜 필요한지. 요청한 사람과 날짜, 관련 결정이나 이슈.' },
      { heading: '할 일', hint: '- 작업 단위 하나씩' },
      { heading: '완료 조건', hint: '- 확인할 수 있는 조건. 실행할 명령, 볼 화면, 받을 응답.' },
    ])
  })
  it('여러 줄 안내는 줄바꿈을 지키고 앞뒤 빈 줄을 뗀다', () => {
    const fields = templateFields({ body: DECISION })
    expect(fields.map((f) => f.heading)).toEqual(['현황', '결정할 것', '선택지', '영향'])
    expect(fields[2].hint).toBe('1. 선택지: 무엇을 뜻하는지와 고른 뒤의 결과\n2. 선택지: 무엇을 뜻하는지와 고른 뒤의 결과\n- 추천: 선택지 번호와 근거')
  })
  it('제목이 없는 본문은 칸이 없다', () => {
    expect(templateFields({ body: '' })).toEqual([])
    expect(templateFields({ body: '그냥 글' })).toEqual([])
  })
})

describe('composeBody', () => {
  const fields = templateFields({ body: FEATURE })
  it('칸마다 "## 제목\\n내용"을 빈 줄로 이어 붙인다', () => {
    expect(composeBody(fields, { 배경: '오늘 요청', '할 일': '- 구현', '완료 조건': '- 테스트 통과' })).toBe(
      '## 배경\n오늘 요청\n\n## 할 일\n- 구현\n\n## 완료 조건\n- 테스트 통과\n',
    )
  })
  it('빈 칸은 제목만 남기고 구조를 지킨다', () => {
    expect(composeBody(fields, { 배경: '내용' })).toBe('## 배경\n내용\n\n## 할 일\n\n## 완료 조건\n')
  })
  it('내용의 앞뒤 공백은 떼고 안의 줄바꿈은 지킨다', () => {
    expect(composeBody(fields, { 배경: '  첫 줄\n둘째 줄  \n' })).toBe('## 배경\n첫 줄\n둘째 줄\n\n## 할 일\n\n## 완료 조건\n')
  })
  it('모든 칸이 비었으면 본문을 비운다', () => {
    expect(composeBody(fields, {})).toBe('')
    expect(composeBody(fields, { 배경: '  ', '할 일': '\n' })).toBe('')
  })
})

describe('hasContent', () => {
  it('공백이 아닌 글이 한 칸이라도 있으면 참이다', () => {
    expect(hasContent({})).toBe(false)
    expect(hasContent({ 배경: ' \n ' })).toBe(false)
    expect(hasContent({ 배경: '', '할 일': 'x' })).toBe(true)
  })
})

describe('toMarkdown', () => {
  const fields = templateFields({ body: FEATURE })
  it('쓴 내용이 있으면 칸을 합친 마크다운을 쓴다', () => {
    expect(toMarkdown({ body: FEATURE }, fields, { 배경: '내용' })).toBe('## 배경\n내용\n\n## 할 일\n\n## 완료 조건\n')
  })
  it('쓴 내용이 없으면 안내가 담긴 템플릿 본문을 그대로 쓴다', () => {
    expect(toMarkdown({ body: FEATURE }, fields, {})).toBe(FEATURE)
  })
})

describe('parseBody', () => {
  const fields = templateFields({ body: FEATURE })
  it('합친 본문은 같은 칸 값으로 되돌아온다', () => {
    const values = { 배경: '오늘 요청\n두 줄', '할 일': '- 구현', '완료 조건': '' }
    const parsed = parseBody(fields, composeBody(fields, values))
    expect(parsed).toEqual({ ok: true, values })
  })
  it('제목의 순서가 달라도 제목별로 칸에 넣는다', () => {
    const parsed = parseBody(fields, '## 완료 조건\n끝\n\n## 배경\n처음\n')
    expect(parsed).toEqual({ ok: true, values: { 배경: '처음', '할 일': '', '완료 조건': '끝' } })
  })
  it('없는 제목은 빈 칸으로 둔다', () => {
    expect(parseBody(fields, '## 할 일\n- 하나\n')).toEqual({ ok: true, values: { 배경: '', '할 일': '- 하나', '완료 조건': '' } })
    expect(parseBody(fields, '')).toEqual({ ok: true, values: { 배경: '', '할 일': '', '완료 조건': '' } })
  })
  it('손대지 않은 안내 문장은 빈 칸으로 본다', () => {
    expect(parseBody(fields, FEATURE)).toEqual({ ok: true, values: { 배경: '', '할 일': '', '완료 조건': '' } })
    const partly = parseBody(fields, FEATURE.replace('- 작업 단위 하나씩', '- 직접 쓴 일'))
    expect(partly).toEqual({ ok: true, values: { 배경: '', '할 일': '- 직접 쓴 일', '완료 조건': '' } })
  })
  it('템플릿에 없는 제목이 있으면 거절하고 그 제목을 알린다', () => {
    const parsed = parseBody(fields, '## 배경\n내용\n\n## 참고\n링크\n')
    expect(parsed.ok).toBe(false)
    expect(!parsed.ok && parsed.reason).toContain('## 참고')
  })
  it('같은 제목이 두 번 있으면 거절한다', () => {
    const parsed = parseBody(fields, '## 배경\nA\n\n## 배경\nB\n')
    expect(parsed.ok).toBe(false)
    expect(!parsed.ok && parsed.reason).toContain('## 배경')
  })
  it('첫 제목 앞에 글이 있으면 거절한다', () => {
    const parsed = parseBody(fields, '머리말\n\n## 배경\n내용\n')
    expect(parsed.ok).toBe(false)
    expect(!parsed.ok && parsed.reason).toContain('앞')
    expect(parseBody(fields, '\n\n## 배경\n내용\n').ok).toBe(true)
  })
  it('코드 블록 안의 "## " 줄은 제목이 아니다', () => {
    const body = '## 배경\n```\n## 참고\n코드\n```\n끝\n'
    const parsed = parseBody(fields, body)
    expect(parsed).toEqual({ ok: true, values: { 배경: '```\n## 참고\n코드\n```\n끝', '할 일': '', '완료 조건': '' } })
  })
  it('3단계 제목과 줄 안의 ## 는 내용으로 둔다', () => {
    const parsed = parseBody(fields, '## 배경\n### 세부\n문장 안의 ## 표시\n')
    expect(parsed).toEqual({ ok: true, values: { 배경: '### 세부\n문장 안의 ## 표시', '할 일': '', '완료 조건': '' } })
  })
  it('줄 끝 공백과 윈도우 줄바꿈을 받아들인다', () => {
    const parsed = parseBody(fields, '## 배경  \r\n내용\r\n\r\n## 할 일\r\n- x\r\n')
    expect(parsed).toEqual({ ok: true, values: { 배경: '내용', '할 일': '- x', '완료 조건': '' } })
  })
})
