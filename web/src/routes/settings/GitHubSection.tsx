import { GitHubConnect } from '@/local/GitHubConnect'
import { lc } from '@/i18n/local'

/** Settings → GitHub (local mode): connect for Push & open PR, the login and scopes, Disconnect. */
export function GitHubSection() {
  return (
    <section aria-labelledby="settings-github">
      <h2 id="settings-github" className="mb-1 text-sm font-bold">
        {lc.github.title}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">{lc.github.hint}</p>
      <GitHubConnect />
    </section>
  )
}
