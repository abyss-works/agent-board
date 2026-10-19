import type { CSSProperties } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router'
import { Toaster } from 'sonner'
import { AppStateProvider } from '@/app/provider'
import { useAppState } from '@/app/state'
import { AppShell } from '@/components/layout/app-shell'
import { backgroundOf } from '@/lib/overlay'
import { ActivityPage } from '@/pages/activity-page'
import { BoardPage } from '@/pages/board-page'
import { DevicePage } from '@/pages/device-page'
import { IssueDetailPage } from '@/pages/issue-detail-page'
import { IssuesPage } from '@/pages/issues-page'
import { LoginPage } from '@/pages/login-page'
import { MilestonesPage } from '@/pages/milestones-page'
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
  const location = useLocation()
  if (!token) return <LoginPage />
  // 이슈 주소이면 그 밑에 그릴 화면의 위치. 이슈를 열어도 아래 화면이 다시 만들어지지 않도록,
  // 오버레이가 없을 때도 같은 방식(location 지정)으로 그린다.
  const background = backgroundOf(location)
  return (
    <>
      {/* 인스턴스를 바꾸면 트리를 새로 만들어 화면 상태가 섞이지 않게 한다. */}
      <Routes key={instance.id} location={background ?? location}>
        <Route element={<AppShell overlayOpen={background !== null} />}>
          <Route index element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<OverviewPage />} />
          <Route path="/board" element={<BoardPage />} />
          <Route path="/issues" element={<IssuesPage />} />
          <Route path="/activity" element={<ActivityPage />} />
          <Route path="/milestones" element={<MilestonesPage />} />
          <Route path="/device" element={<DevicePage />} />
          <Route path="*" element={<Navigate to="/overview" replace />} />
        </Route>
      </Routes>
      {background ? (
        <Routes>
          <Route path="/issues/:key" element={<IssueDetailPage />} />
        </Routes>
      ) : null}
    </>
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
