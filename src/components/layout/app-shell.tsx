import { Outlet } from 'react-router'
import { useMenuShortcuts } from '@/hooks/use-menu-shortcuts'
import { Sidebar } from './sidebar'

export function AppShell({ overlayOpen }: { overlayOpen: boolean }) {
  useMenuShortcuts(overlayOpen)
  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <Outlet />
      </main>
    </div>
  )
}
