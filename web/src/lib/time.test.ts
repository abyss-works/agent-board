import { describe, expect, it } from 'vitest'
import { formatKst, formatKstDate, formatKstTime, formatRelative } from './time'

describe('formatKst', () => {
  it('UTC 시각을 KST(+9)로 바꿔 날짜와 시각을 적는다', () => {
    expect(formatKst('2026-10-17T05:46:06.123Z')).toBe('2026-10-17 14:46:06')
  })
  it('자정을 넘기면 날짜도 바뀐다', () => {
    expect(formatKst('2026-10-17T16:30:00.000Z')).toBe('2026-10-18 01:30:00')
  })
  it('날짜만, 시각만 따로 적는다', () => {
    expect(formatKstDate('2026-10-17T16:30:00.000Z')).toBe('2026-10-18')
    expect(formatKstTime('2026-10-17T16:30:00.000Z')).toBe('01:30:00')
  })
  it('잘못된 값은 그대로 돌려준다', () => {
    expect(formatKst('nope')).toBe('nope')
  })
})

describe('formatRelative', () => {
  const now = new Date('2026-10-17T12:00:00.000Z')
  it('1분 안은 방금', () => {
    expect(formatRelative('2026-10-17T11:59:30.000Z', now)).toBe('방금')
  })
  it('분, 시간, 일 단위', () => {
    expect(formatRelative('2026-10-17T11:57:00.000Z', now)).toBe('3분 전')
    expect(formatRelative('2026-10-17T09:00:00.000Z', now)).toBe('3시간 전')
    expect(formatRelative('2026-10-15T12:00:00.000Z', now)).toBe('2일 전')
  })
  it('7일이 넘으면 KST 날짜', () => {
    expect(formatRelative('2026-10-01T16:00:00.000Z', now)).toBe('2026-10-02')
  })
  it('미래 시각은 방금으로 본다', () => {
    expect(formatRelative('2026-10-17T12:00:05.000Z', now)).toBe('방금')
  })
})
