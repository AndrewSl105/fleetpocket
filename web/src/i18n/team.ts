// Strings of team mode (Settings → Team, My keys, Telegram, Profile, signup).
// Kept out of en.ts so they load with the screens that use them.
import type { Agent, Role } from '@/api/types'

export const tm = {
  // Single-owner mode: the only team UI.
  createTitle: 'Create a team',
  createBody: 'Invite people to this install. You become the first admin and sign in as before; everyone brings their own agent keys.',
  createCta: 'Create a team',
  createEmail: 'Your email',
  createName: 'Your name',
  createConfirm: 'Create team',
  created: 'Team created. You are the admin.',
  needsSecretsKey: 'Team mode needs SECRETS_KEY on the server (rerun deploy/install.sh).',

  teamTitle: 'Team',
  teamHint: 'One install is one team. Keys stay with each person.',
  people: 'People',
  you: 'you',
  owner: 'owner',
  disabled: 'disabled',
  pending: 'not enrolled',
  roles: { admin: 'Admin', member: 'Member', viewer: 'Viewer' } satisfies Record<Role, string>,
  roleHint: {
    admin: 'Everything, including people, projects and settings',
    member: 'Creates tasks, schedules and ideas; acts on their own tasks',
    viewer: 'Reads everything, changes nothing',
  } satisfies Record<Role, string>,
  changeRole: 'Role',
  disable: 'Disable',
  enable: 'Enable',
  remove: 'Remove',
  disableTitle: (name: string) => `Disable ${name}?`,
  disableBody: 'Their sessions and computer tokens are revoked, their keys deleted and their queued tasks cancelled. History stays.',
  liveTasks: 'Their running tasks',
  liveFinish: 'Let them finish',
  liveCancel: 'Cancel them',
  removeTitle: (name: string) => `Remove ${name}?`,
  removeBody: 'Their tasks stay, without an author.',
  budget: 'Monthly budget',
  budgetNone: 'No budget',
  budgetSave: 'Save budget',

  fallback: "Allow the server's shared keys as a fallback",
  fallbackHint: 'Tasks of people without their own key use the env keys. Off by default.',
  subscriptionWarning:
    'Subscription keys (CLAUDE_CODE_OAUTH_TOKEN) are personal: do not share one. For a shared company key, set an API key on the server and turn the fallback on.',

  invites: 'Invites',
  invite: 'Invite someone',
  inviteRole: 'Role',
  inviteEmail: 'Email (optional, locks the link to it)',
  inviteExpiry: 'Expires in (days)',
  inviteCreate: 'Create link',
  inviteLink: 'Invite link (shown once)',
  inviteCopy: 'Copy link',
  inviteRevoke: 'Revoke',
  inviteExpires: (when: string) => `expires ${when}`,
  inviteUsed: 'used',
  inviteRevoked: 'revoked',
  noInvites: 'No open invites.',

  audit: 'Audit log',
  auditEmpty: 'Nothing recorded yet.',
  auditFilter: 'Filter by action',

  turnOff: 'Turn team mode off',
  turnOffBody: 'Only possible when you are the last active person. You then sign in as the single owner.',
  turnedOff: 'Team mode is off.',

  // My keys
  keysTitle: 'My keys',
  keysHint: 'Your tasks run with your own agent keys. Values are write-only; only the last 4 characters come back.',
  providers: { claude: 'Claude', codex: 'Codex (OpenAI)', cursor: 'Cursor' } satisfies Record<Agent, string>,
  kinds: { oauth_token: 'Subscription token (CLAUDE_CODE_OAUTH_TOKEN)', api_key: 'API key' },
  keyValue: 'Key',
  keyLabel: 'Label (optional)',
  keySave: 'Save key',
  keyAdd: 'Add key',
  keyReplace: 'Replace',
  keyRemove: 'Remove',
  keyTest: 'Test',
  keyNone: 'No key',
  keySaved: 'Key saved',
  keyRemoved: 'Key removed',
  lastUsed: (when: string) => `used ${when}`,
  neverUsed: 'never used',
  fallbackOn: 'No key? Your tasks use the shared server key.',
  fallbackOff: 'No key? Your tasks wait with "needs key".',

  // Telegram
  tgTitle: 'Telegram',
  tgLinked: 'Your Telegram account is linked: the bot acts as you and sends your notices.',
  tgNotLinked: 'Link Telegram to get your questions and notices and to use the bot as yourself.',
  tgGetCode: 'Get a link code',
  tgSend: 'Send this to the bot within 10 minutes:',
  tgUnlink: 'Unlink',
  tgUnlinked: 'Telegram unlinked',
  tgNoBot: 'The server has no Telegram bot configured.',

  // Profile
  profileTitle: 'Profile',
  name: 'Name',
  email: 'Email',
  githubLogin: 'GitHub login',
  commitEmail: 'Commit email',
  commitEmailHint: 'Adds Co-authored-by to commits made for you',
  saveProfile: 'Save profile',
  profileSaved: 'Profile saved',
  password: 'Change password',
  currentPassword: 'Current password',
  newPassword: 'New password (at least 12 characters)',
  savePassword: 'Change password',
  passwordSaved: 'Password changed; your other sessions are signed out',
  totpReset: 'Reset two-factor code',
  totpScan: 'Scan this in your authenticator app, then enter a code from it.',
  totpConfirm: 'Confirm',
  totpSaved: 'Two-factor code replaced',

  signedInAs: (name: string) => `Signed in as ${name}`,

  shared: 'shared',
  shareNode: 'Share with the team',

  // Filters
  mine: 'Mine',
  everyone: 'Everyone',
  person: 'Person',
  anyone: 'Anyone',

  // needs_key
  needsKey: (provider: string) => `Add your ${provider} key in My keys`,
  openKeys: 'Open My keys',

  // Signup
  signupTitle: 'Join the team',
  signupIntro: (role: string) => `You are invited as ${role}.`,
  signupPassword: 'Password (at least 12 characters)',
  signupNext: 'Next: two-factor code',
  signupScan: 'Scan this QR code with your authenticator app (or enter the secret), then type the 6-digit code.',
  signupSecret: 'Secret',
  signupCode: 'Code from the app',
  signupFinish: 'Create account',
  signupBadLink: 'This invite link is invalid, used, revoked or expired. Ask your admin for a new one.',
  signupOpening: 'Opening the invite…',
} as const

export function roleLabel(r: Role): string {
  return tm.roles[r]
}
