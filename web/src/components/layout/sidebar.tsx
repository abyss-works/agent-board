import { ActivityIcon, ChevronsUpDownIcon, FolderIcon, FolderTreeIcon, KanbanIcon, LayoutDashboardIcon, ListIcon, LogOutIcon, MilestoneIcon, PlusIcon, SettingsIcon, Trash2Icon } from 'lucide-react'
import { useState, type ComponentType, type SVGProps } from 'react'
import { NavLink } from 'react-router'
import { useProjects } from '@/api/queries'
import type { Project } from '@/api/types'
import { useConnection } from '@/app/connection'
import { ACTOR, POLL_MS, useAppState } from '@/app/state'
import { IssueCreateDialog } from '@/components/issue/issue-create-dialog'
import { ProjectDeleteDialog } from '@/components/project/project-delete-dialog'
import { ProjectDialog } from '@/components/project/project-dialog'
import { TopicManageDialog } from '@/components/project/topic-manage-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Kbd } from '@/components/ui/kbd'
import { PROJECT_STATUS_LABEL } from '@/lib/labels'
import { MENU_SHORTCUTS } from '@/lib/shortcuts'
import { formatKstTime } from '@/lib/time'
import { cn } from '@/lib/utils'

const MENU_ICON: Record<string, ComponentType<SVGProps<SVGSVGElement>>> = {
  '/overview': LayoutDashboardIcon,
  '/board': KanbanIcon,
  '/issues': ListIcon,
  '/activity': ActivityIcon,
  '/milestones': MilestoneIcon,
}

function InstanceSwitcher() {
  const { instances, instance, selectInstance } = useAppState()
  const single = instances.length === 1
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={single}>
        <button
          type="button"
          className={cn(
            'flex h-9 w-full items-center gap-2 rounded-md px-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring',
            !single && 'cursor-pointer hover:bg-sidebar-accent',
          )}
        >
          <div className="flex size-6 shrink-0 items-center justify-center rounded-md bg-primary text-[0.6875rem] font-bold text-primary-foreground">ab</div>
          <div className="min-w-0 flex-1 leading-tight">
            <div className="truncate text-[0.8125rem] font-semibold">agent-board</div>
            <div className="truncate font-mono text-[0.625rem] text-muted-foreground">
              {single ? instance.description : `${instance.label} · ${instance.description}`}
            </div>
          </div>
          {!single ? <ChevronsUpDownIcon className="size-3.5 text-muted-foreground" /> : null}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuLabel>인스턴스</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={instance.id} onValueChange={selectInstance}>
          {instances.map((i) => (
            <DropdownMenuRadioItem key={i.id} value={i.id}>
              <span className="font-medium">{i.label}</span>
              <span className="ml-auto font-mono text-[0.6875rem] text-muted-foreground">{i.description}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function ProjectSwitcher({
  onCreate,
  onEdit,
  onTopics,
  onDelete,
}: {
  onCreate: () => void
  onEdit: (p: Project) => void
  onTopics: (p: Project) => void
  onDelete: (p: Project) => void
}) {
  const { project, selectProject } = useAppState()
  const projects = useProjects()
  const items = projects.data?.items ?? []
  const current = items.find((p) => p.key === project) ?? null
  const sorted = [...items].sort((a, b) => (a.status === b.status ? a.key.localeCompare(b.key) : a.status === 'active' ? -1 : 1))

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="flex h-8 w-full cursor-pointer items-center gap-2 rounded-md border border-sidebar-border bg-sidebar-accent/50 px-2 text-left text-[0.8125rem] outline-none hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-ring"
        >
          <FolderIcon className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">
            {project ? (
              <>
                <span className="font-mono text-xs font-medium">{project}</span>
                {current ? <span className="ml-1.5 text-muted-foreground">{current.name}</span> : null}
              </>
            ) : (
              '전체 프로젝트'
            )}
          </span>
          {current && current.status !== 'active' ? <Badge variant="warning">{PROJECT_STATUS_LABEL[current.status]}</Badge> : null}
          <ChevronsUpDownIcon className="size-3.5 shrink-0 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[var(--radix-dropdown-menu-trigger-width)] min-w-56">
        <DropdownMenuLabel>프로젝트</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={project ?? ''} onValueChange={(v) => selectProject(v || null)}>
          <DropdownMenuRadioItem value="">전체 프로젝트</DropdownMenuRadioItem>
          {sorted.map((p) => (
            <DropdownMenuRadioItem key={p.key} value={p.key}>
              <span className="font-mono text-xs font-medium">{p.key}</span>
              <span className="truncate text-muted-foreground">{p.name}</span>
              {p.status !== 'active' ? (
                <Badge variant="outline" className="ml-auto">
                  {PROJECT_STATUS_LABEL[p.status]}
                </Badge>
              ) : null}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        {projects.isError ? <div className="px-2 py-1.5 text-[0.6875rem] text-destructive">프로젝트 목록을 불러오지 못했습니다.</div> : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onCreate}>
          <PlusIcon />새 프로젝트
        </DropdownMenuItem>
        {current ? (
          <>
            <DropdownMenuItem onSelect={() => onEdit(current)}>
              <SettingsIcon />
              {current.key} 설정
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onTopics(current)}>
              <FolderTreeIcon />
              {current.key} 주제 관리
            </DropdownMenuItem>
            {current.status !== 'deleted' ? (
              <DropdownMenuItem variant="destructive" onSelect={() => onDelete(current)}>
                <Trash2Icon />
                {current.key} 삭제
              </DropdownMenuItem>
            ) : null}
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function ConnectionStatus() {
  const status = useConnection()
  if (status.error) {
    return (
      <div className="flex items-start gap-1.5 text-[0.6875rem] text-rose-400">
        <span className="mt-1 size-1.5 shrink-0 rounded-full bg-rose-400" />
        <span className="leading-snug">{status.error.message}</span>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-1.5 text-[0.6875rem] text-muted-foreground">
      <span className="relative flex size-1.5">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-400 opacity-60" />
        <span className="relative inline-flex size-1.5 rounded-full bg-emerald-400" />
      </span>
      {POLL_MS / 1000}초마다 갱신
      {status.lastOkAt ? <span className="ml-auto font-mono tabular-nums">{formatKstTime(new Date(status.lastOkAt).toISOString())}</span> : null}
    </div>
  )
}

export function Sidebar() {
  const { logout, project, selectProject } = useAppState()
  const [newIssue, setNewIssue] = useState(false)
  const [createProject, setCreateProject] = useState(false)
  const [editProject, setEditProject] = useState<Project | null>(null)
  const [topicProject, setTopicProject] = useState<Project | null>(null)
  const [deleteProject, setDeleteProject] = useState<Project | null>(null)

  return (
    <aside className="flex w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground">
      <div className="p-2">
        <InstanceSwitcher />
      </div>
      <div className="space-y-2 px-3">
        <ProjectSwitcher onCreate={() => setCreateProject(true)} onEdit={setEditProject} onTopics={setTopicProject} onDelete={setDeleteProject} />
        <Button variant="secondary" size="sm" className="w-full justify-start" onClick={() => setNewIssue(true)}>
          <PlusIcon />새 이슈
        </Button>
      </div>

      <nav className="mt-4 space-y-0.5 px-2">
        {MENU_SHORTCUTS.map((m) => {
          const Icon = MENU_ICON[m.path]
          return (
            <NavLink
              key={m.path}
              to={m.path}
              className={({ isActive }) =>
                cn(
                  'flex h-8 items-center gap-2.5 rounded-md px-2 text-[0.8125rem] transition-colors',
                  isActive ? 'bg-sidebar-accent font-medium text-sidebar-accent-foreground' : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground',
                )
              }
            >
              <Icon className="size-4" />
              <span className="flex-1">{m.label}</span>
              <Kbd>{m.key}</Kbd>
            </NavLink>
          )
        })}
      </nav>

      <div className="mt-auto space-y-2 border-t border-sidebar-border p-3">
        <ConnectionStatus />
        <div className="flex items-center gap-2 text-[0.6875rem] text-muted-foreground">
          <span className="size-2 rounded-full bg-sky-400" />
          행위자 <span className="font-mono text-foreground/80">{ACTOR}</span>
          <Button variant="ghost" size="xs" className="ml-auto" onClick={logout} title="토큰을 지우고 로그인 화면으로">
            <LogOutIcon />
            로그아웃
          </Button>
        </div>
      </div>

      <IssueCreateDialog open={newIssue} onOpenChange={setNewIssue} />
      <ProjectDialog open={createProject} onOpenChange={setCreateProject} onCreated={(p) => selectProject(p.key)} />
      <ProjectDialog open={editProject !== null} onOpenChange={(o) => !o && setEditProject(null)} project={editProject ?? undefined} />
      <TopicManageDialog open={topicProject !== null} onOpenChange={(o) => !o && setTopicProject(null)} project={topicProject} />
      <ProjectDeleteDialog
        open={deleteProject !== null}
        onOpenChange={(o) => !o && setDeleteProject(null)}
        project={deleteProject}
        onDeleted={(p) => {
          if (p.key === project) selectProject(null)
        }}
      />
    </aside>
  )
}
