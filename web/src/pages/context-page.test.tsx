import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { ContextItem, ContextNeighbor } from '@/api/context-types'
import { NeighborGraph } from './context-page'

const n = (edge_id: number, direction: ContextNeighbor['direction'], key: string): ContextNeighbor => ({
  edge_id, direction, kind: 'about', origin: 'explicit', status: 'confirmed', node_type: 'issue', node_id: edge_id, key_or_name: key, title: '제목', summary: '', content: null,
})

const item = (neighbors: ContextNeighbor[]): ContextItem => ({
  node_type: 'spec', node_id: 1, project: 'APP', key_or_name: 'api', kind: 'spec', title: 'API', score: 0.9, summary: '', content: '', superseded_by: null, neighbors,
})

const render = (it: ContextItem) => renderToStaticMarkup(<MemoryRouter><NeighborGraph item={it} /></MemoryRouter>)

describe('NeighborGraph', () => {
  it('들어오는 연결과 나가는 연결을 나눠 보인다', () => {
    const html = render(item([n(1, 'inbound', 'APP-1'), n(2, 'outbound', 'APP-2')]))
    expect(html).toContain('들어오는 연결 1')
    expect(html).toContain('나가는 연결 1')
    expect(html).toContain('/issues/APP-1')
    expect(html).toContain('/issues/APP-2')
  })
  it('연결이 없으면 안내한다', () => {
    expect(render(item([]))).toContain('확정된 연결이 없습니다')
  })
})
