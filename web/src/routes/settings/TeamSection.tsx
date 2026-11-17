import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Copy, Users } from 'lucide-react'
import { errorMessage } from '@/api/hooks'
import {
  useAudit,
  useCreateInvite,
  useCurrentUser,
  useDisableTeam,
  useEnableTeam,
  useInvites,
  useRemoveUser,
  useRevokeInvite,
  useSetFallback,
  useTeamStatus,
  useUpdateUser,
  useUsers,
} from '@/api/team'
import type { Role, TeamUser } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { UserBadge } from '@/components/UserBadge'
import { useConfirm } from '@/components/confirm'
import { copyText } from '@/components/CopyButton'
import { dateTime, relativeTime } from '@/lib/format'
import { haptic } from '@/telegram/haptics'
import { tm } from '@/i18n/team'

const ROLES: Role[] = ['admin', 'member', 'viewer']

function fail(e: unknown) {
  haptic.error()
  toast.error(errorMessage(e))
}

/** Single-owner mode: the only team UI, a "Create a team" card (admin = the owner). */
function CreateTeamCard() {
  const enable = useEnableTeam()
  const status = useTeamStatus()
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')

  async function submit(e: FormEvent) {
    e.preventDefault()
    try {
      await enable.mutateAsync({ email, name })
      haptic.success()
      toast.success(tm.created)
    } catch (err) {
      fail(err)
    }
  }

  return (
    <section aria-labelledby="team-title" id="team">
      <h2 id="team-title" className="mb-1 text-sm font-bold">
        {tm.createTitle}
      </h2>
      <div className="fm-group space-y-3 px-3.5 py-3">
        <p className="text-sm">{tm.createBody}</p>
        {status.data && !status.data.secrets_key && <p className="text-xs text-danger">{tm.needsSecretsKey}</p>}
        {!open ? (
          <Button className="h-11 w-full" onClick={() => setOpen(true)} disabled={status.data ? !status.data.secrets_key : false}>
            <Users /> {tm.createCta}
          </Button>
        ) : (
          <form onSubmit={submit} className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="team-name">{tm.createName}</Label>
              <Input id="team-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" className="h-10" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="team-email">{tm.createEmail}</Label>
              <Input id="team-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" className="h-10" />
            </div>
            <Button type="submit" className="h-11 w-full" disabled={enable.isPending || !email.trim() || !name.trim()}>
              {tm.createConfirm}
            </Button>
          </form>
        )}
      </div>
    </section>
  )
}

function UserRow({ user, me }: { user: TeamUser; me?: number }) {
  const update = useUpdateUser()
  const remove = useRemoveUser()
  const confirm = useConfirm()
  const [live, setLive] = useState<'finish' | 'cancel'>('finish')
  const [budget, setBudget] = useState(user.monthly_budget_usd != null ? String(user.monthly_budget_usd) : '')
  const self = user.id === me

  async function disable() {
    if (!(await confirm(`${tm.disableTitle(user.name)} ${tm.disableBody}`, tm.disable))) return
    update.mutate({ id: user.id, disabled: true, live_tasks: live }, { onError: fail })
  }
  async function del() {
    if (!(await confirm(`${tm.removeTitle(user.name)} ${tm.removeBody}`, tm.remove))) return
    remove.mutate(user.id, { onError: fail })
  }
  function saveBudget() {
    const v = budget.trim()
    update.mutate(v ? { id: user.id, monthly_budget_usd: Number(v) } : { id: user.id, clear_budget: true }, {
      onError: fail,
      onSuccess: () => toast.success(tm.budgetSave),
    })
  }

  return (
    <li className="space-y-2 px-3.5 py-3" data-testid={`user-${user.email}`}>
      <div className="flex min-w-0 items-center gap-2">
        <UserBadge user={{ id: user.id, name: user.name, initials: user.initials }} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">
            {user.name}
            {self && <span className="text-muted-foreground"> ({tm.you})</span>}
          </p>
          <p className="truncate font-mono text-xs text-muted-foreground">
            {user.email}
            {user.is_owner && ` · ${tm.owner}`}
            {user.disabled_at ? ` · ${tm.disabled}` : !user.active ? ` · ${tm.pending}` : ''}
          </p>
        </div>
        <select
          aria-label={`${tm.changeRole}: ${user.name}`}
          value={user.role}
          disabled={user.is_owner || !!user.disabled_at || update.isPending}
          onChange={(e) => update.mutate({ id: user.id, role: e.target.value as Role }, { onError: fail })}
          className="fm-field h-9 bg-background px-2 text-xs"
        >
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {tm.roles[r]}
            </option>
          ))}
        </select>
      </div>
      {!user.is_owner && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <Input
            aria-label={`${tm.budget}: ${user.name}`}
            inputMode="decimal"
            placeholder={tm.budgetNone}
            value={budget}
            onChange={(e) => setBudget(e.target.value.replace(/[^0-9.]/g, ''))}
            className="h-9 w-28"
          />
          <Button variant="outline" className="h-9" onClick={saveBudget} disabled={update.isPending}>
            {tm.budgetSave}
          </Button>
          <span className="flex-1" />
          {user.disabled_at ? (
            <>
              <Button variant="outline" className="h-9" onClick={() => update.mutate({ id: user.id, disabled: false }, { onError: fail })}>
                {tm.enable}
              </Button>
              <Button variant="outline" className="h-9 text-danger" onClick={del}>
                {tm.remove}
              </Button>
            </>
          ) : (
            !self && (
              <>
                <select
                  aria-label={tm.liveTasks}
                  value={live}
                  onChange={(e) => setLive(e.target.value as 'finish' | 'cancel')}
                  className="fm-field h-9 bg-background px-2 text-xs"
                >
                  <option value="finish">{tm.liveFinish}</option>
                  <option value="cancel">{tm.liveCancel}</option>
                </select>
                <Button variant="outline" className="h-9 text-danger" onClick={disable}>
                  {tm.disable}
                </Button>
              </>
            )
          )}
        </div>
      )}
    </li>
  )
}

function Invites() {
  const invites = useInvites()
  const create = useCreateInvite()
  const revoke = useRevokeInvite()
  const [role, setRole] = useState<Role>('member')
  const [email, setEmail] = useState('')
  const [days, setDays] = useState('7')
  const [link, setLink] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    try {
      const r = await create.mutateAsync({ role, email: email.trim() || null, expires_in_hours: Math.max(1, Number(days) || 7) * 24 })
      // The server builds the link from WEB_APP_URL; show it once.
      setLink(r.url)
      setEmail('')
      haptic.success()
    } catch (err) {
      fail(err)
    }
  }

  const open = (invites.data ?? []).filter((i) => !i.used_at && !i.revoked_at && new Date(i.expires_at) > new Date())
  return (
    <div className="space-y-3">
      <h3 className="text-sm font-bold">{tm.invites}</h3>
      <form onSubmit={submit} className="fm-group space-y-3 px-3.5 py-3">
        <p className="text-sm font-medium">{tm.invite}</p>
        <div className="grid grid-cols-2 gap-2">
          <div className="space-y-1">
            <Label htmlFor="invite-role">{tm.inviteRole}</Label>
            <select id="invite-role" value={role} onChange={(e) => setRole(e.target.value as Role)} className="fm-field h-10 w-full bg-background px-2 text-sm">
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {tm.roles[r]}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="invite-days">{tm.inviteExpiry}</Label>
            <Input id="invite-days" inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))} className="h-10" />
          </div>
        </div>
        <p className="text-xs text-muted-foreground">{tm.roleHint[role]}</p>
        <div className="space-y-1">
          <Label htmlFor="invite-email">{tm.inviteEmail}</Label>
          <Input id="invite-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} className="h-10" />
        </div>
        <Button type="submit" className="h-11 w-full" disabled={create.isPending}>
          {tm.inviteCreate}
        </Button>
        {link && (
          <div className="space-y-1">
            <Label htmlFor="invite-link">{tm.inviteLink}</Label>
            <div className="flex gap-2">
              <Input id="invite-link" readOnly value={link} className="h-10 font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
              <Button type="button" variant="outline" className="h-10" aria-label={tm.inviteCopy} onClick={() => void copyText(link)}>
                <Copy />
              </Button>
            </div>
          </div>
        )}
      </form>
      {open.length === 0 ? (
        <p className="text-xs text-muted-foreground">{tm.noInvites}</p>
      ) : (
        <ul className="divide-y fm-group">
          {open.map((i) => (
            <li key={i.id} className="flex items-center gap-2 px-3.5 py-2 text-xs">
              <span className="font-semibold">{tm.roles[i.role]}</span>
              <span className="min-w-0 flex-1 truncate text-muted-foreground">
                {i.email ?? '—'} · {tm.inviteExpires(relativeTime(i.expires_at))}
              </span>
              <Button variant="outline" className="h-9" onClick={() => revoke.mutate(i.id, { onError: fail })}>
                {tm.inviteRevoke}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function AuditLog() {
  const [open, setOpen] = useState(false)
  const [action, setAction] = useState('')
  const audit = useAudit(action, open)
  return (
    <details className="fm-group px-3.5 py-2" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="flex min-h-11 cursor-pointer items-center text-sm font-bold">{tm.audit}</summary>
      <Input aria-label={tm.auditFilter} placeholder={tm.auditFilter} value={action} onChange={(e) => setAction(e.target.value.trim())} className="mb-2 h-9" />
      {audit.data ? (
        audit.data.items.length === 0 ? (
          <p className="pb-2 text-xs text-muted-foreground">{tm.auditEmpty}</p>
        ) : (
          <ul className="divide-y text-xs">
            {audit.data.items.map((a) => (
              <li key={a.id} className="py-1.5">
                <span className="font-mono font-semibold">{a.action}</span>
                {a.target_id && <span className="font-mono"> {a.target_type}:{a.target_id}</span>}
                <span className="block text-muted-foreground">
                  {dateTime(a.at)}
                  {a.actor_user_id != null && ` · #${a.actor_user_id}`}
                </span>
              </li>
            ))}
          </ul>
        )
      ) : open ? (
        <Skeleton className="h-16" />
      ) : null}
    </details>
  )
}

/** Settings → Team: admins only, team mode on. */
function TeamPanel() {
  const status = useTeamStatus()
  const users = useUsers()
  const fallback = useSetFallback()
  const disable = useDisableTeam()
  const confirm = useConfirm()
  const me = useCurrentUser()

  async function turnOff() {
    if (!(await confirm(tm.turnOffBody, tm.turnOff))) return
    try {
      await disable.mutateAsync()
      toast.success(tm.turnedOff)
    } catch (e) {
      fail(e)
    }
  }

  return (
    <section aria-labelledby="team-title" id="team" className="space-y-4">
      <div>
        <h2 id="team-title" className="mb-1 text-sm font-bold">
          {tm.teamTitle}
        </h2>
        <p className="text-xs text-muted-foreground">{tm.teamHint}</p>
      </div>
      <div>
        <h3 className="mb-2 text-sm font-bold">{tm.people}</h3>
        {users.data ? (
          <ul className="divide-y fm-group">
            {users.data.map((u) => (
              <UserRow key={u.id} user={u} me={me?.id} />
            ))}
          </ul>
        ) : (
          <Skeleton className="h-24" />
        )}
      </div>
      <Invites />
      {status.data && (
        <div className="space-y-2">
          <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
            <span className="min-w-0">
              <span className="block text-sm font-medium">{tm.fallback}</span>
              <span className="block text-xs text-muted-foreground">{tm.fallbackHint}</span>
            </span>
            <Switch
              checked={status.data.shared_key_fallback}
              disabled={fallback.isPending}
              onCheckedChange={(on) => fallback.mutate(on, { onError: fail })}
              aria-label={tm.fallback}
            />
          </label>
          <p className="text-xs text-muted-foreground">{tm.subscriptionWarning}</p>
        </div>
      )}
      <AuditLog />
      <Button variant="outline" className="h-11 w-full" onClick={turnOff} disabled={disable.isPending}>
        {tm.turnOff}
      </Button>
    </section>
  )
}

/**
 * Settings → Team. Single-owner mode shows only the "Create a team" card;
 * local mode shows nothing; members and viewers see nothing here (they have
 * My keys, Telegram and Profile).
 */
export function TeamSection({ teamEnabled, isAdmin, local }: { teamEnabled: boolean; isAdmin: boolean; local: boolean }) {
  if (local || !isAdmin) return null
  return teamEnabled ? <TeamPanel /> : <CreateTeamCard />
}
