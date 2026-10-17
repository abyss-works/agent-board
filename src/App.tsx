import type { CSSProperties } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'
import { Toaster } from 'sonner'
import { AppStateProvider } from '@/app/provider'
import { useAppState } from '@/app/state'
import { AppShell } from '@/components/layout/app-shell'
import { ActivityPage } from '@/pages/activity-page'
import { BoardPage } from '@/pages/board-page'
import { IssueDetailPage } from '@/pages/issue-detail-page'
import { IssuesPage } from '@/pages/issues-page'
import { LoginPage } from '@/pages/login-page'
import { OverviewPage } from '@/pages/overview-page'

// sonner는 px로 고정한 크기를 레이어 밖 CSS로 넣는다. 루트 글꼴 크기를 따르도록 기본 px 값을 rem(÷16)으로 덮어쓴다.
// 클래스 끝의 !는 레이어 밖 CSS보다 우선하게 하려는 것이다.
const TOAST_STYLE = {
  '--width': '22.25rem',
  '--border-radius': '0.5rem',
  '--toast-icon-margin-start': '-0.1875rem',
  '--toast-icon-margin-end': '0.25rem',
  '--toast-svg-margin-start': '-0.0625rem',
} as CSSProperties

const TOAST_CLASS_NAMES = {
  toast: 'gap-1.5! p-4! text-[0.8125rem]!',
  icon: 'size-4! [&_svg]:size-5!',
  content: 'gap-0.5!',
  closeButton: 'size-5! [&_svg]:size-3!',
}

function Root() {
  const { token, instance } = useAppState()
  if (!token) return <LoginPage />
  return (
    // 인스턴스를 바꾸면 트리를 새로 만들어 화면 상태가 섞이지 않게 한다.
    <Routes key={instance.id}>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="/overview" element={<OverviewPage />} />
        <Route path="/board" element={<BoardPage />} />
        <Route path="/issues" element={<IssuesPage />} />
        <Route path="/issues/:key" element={<IssueDetailPage />} />
        <Route path="/activity" element={<ActivityPage />} />
        <Route path="*" element={<Navigate to="/overview" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return (
    <AppStateProvider>
      <BrowserRouter>
        <Root />
      </BrowserRouter>
      <Toaster
        theme="dark"
        position="bottom-right"
        richColors
        closeButton
        offset="1.5rem"
        mobileOffset="1rem"
        style={TOAST_STYLE}
        toastOptions={{ classNames: TOAST_CLASS_NAMES }}
      />
    </AppStateProvider>
  )
}
