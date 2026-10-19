import { describe, expect, it } from 'vitest'
import { decisionForCreate, decisionPatch } from './decision'

describe('decisionForCreate', () => {
  it('비었거나 공백뿐이면 아무것도 보내지 않는다', () => {
    expect(decisionForCreate('')).toEqual({})
    expect(decisionForCreate('  \n ')).toEqual({})
  })
  it('쓴 내용은 앞뒤 공백을 떼어 보낸다', () => {
    expect(decisionForCreate('  캐시를 쓸지 정해 주세요  ')).toEqual({ decision_needed: '캐시를 쓸지 정해 주세요' })
  })
  it('생성 요청 본문에는 채웠을 때만 decision_needed 키가 실린다', () => {
    const wire = (text: string) => JSON.parse(JSON.stringify({ project: 'AGB', title: '제목', ...decisionForCreate(text) })) as Record<string, unknown>
    expect(wire('')).toEqual({ project: 'AGB', title: '제목' })
    expect(wire('질문')).toEqual({ project: 'AGB', title: '제목', decision_needed: '질문' })
  })
})

describe('decisionPatch', () => {
  it('고친 내용은 앞뒤 공백을 떼어 보낸다', () => {
    expect(decisionPatch('  질문  ')).toEqual({ decision_needed: '질문' })
  })
  it('비우면 null을 보내 지운다', () => {
    expect(decisionPatch('')).toEqual({ decision_needed: null })
    expect(decisionPatch('  ')).toEqual({ decision_needed: null })
  })
  it('지울 때도 키가 요청 본문에 남는다', () => {
    expect(JSON.stringify(decisionPatch(''))).toBe('{"decision_needed":null}')
  })
})
