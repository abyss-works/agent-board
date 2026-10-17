import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { isExternalUrl, safeUrl } from '@/lib/sanitize'
import { cn } from '@/lib/utils'

const components: Components = {
  a({ href, children, ...rest }) {
    const target = href ?? ''
    if (isExternalUrl(target)) {
      return (
        <a href={target} target="_blank" rel="noopener noreferrer" {...rest}>
          {children}
        </a>
      )
    }
    return (
      <a href={target || undefined} {...rest}>
        {children}
      </a>
    )
  },
}

/**
 * 에이전트가 쓴 마크다운을 그린다. 원시 HTML은 글자로 보여 주고(실행하지 않음) 위험한 URL은 비운다.
 */
export function Markdown({ source, className }: { source: string; className?: string }) {
  return (
    <div className={cn('prose-agb', className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} urlTransform={safeUrl} components={components}>
        {source}
      </ReactMarkdown>
    </div>
  )
}
