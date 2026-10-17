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
      <Toaster theme="dark" position="bottom-right" richColors closeButton toastOptions={{ className: 'text-[13px]' }} />
    </AppStateProvider>
  )
}
