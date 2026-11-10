import { Button } from '@/components/ui/button'

/** 서버에 사용자 행위자가 여럿일 때 이 브라우저에서 쓸 행위자를 고른다. */
export function ActorChoice({ users, onPick }: { users: string[]; onPick: (actor: string) => void }) {
  return (
    <div className="space-y-1.5">
      <p className="text-[0.8125rem] font-medium">어느 행위자로 쓸까요?</p>
      <p className="text-[0.6875rem] text-muted-foreground">고른 행위자는 이 브라우저에 저장되고 모든 요청에 쓰입니다.</p>
      <div className="flex flex-col gap-1.5 pt-1">
        {users.map((u) => (
          <Button key={u} type="button" variant="outline" className="justify-start font-mono" onClick={() => onPick(u)}>
            {u}
          </Button>
        ))}
      </div>
    </div>
  )
}
