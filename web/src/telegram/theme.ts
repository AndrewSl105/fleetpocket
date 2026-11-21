import { supports, type WebApp } from './webapp'

/** Reads a colour token from src/index.css, e.g. --jungle → "#14170f". */
function token(root: HTMLElement, name: string, fallback: string) {
  return getComputedStyle(root).getPropertyValue(name).trim() || fallback
}

/**
 * The Mini App is dark only, like the browser app: Telegram's colorScheme and
 * themeParams are not followed, and the native chrome is painted with the
 * jungle background (docs/DECISIONS.md, Web).
 */
export function applyTelegramTheme(tg: WebApp, root: HTMLElement = document.documentElement) {
  root.classList.add('dark')
  root.style.colorScheme = 'dark'

  const jungle = token(root, '--jungle', '#14170f')
  try {
    if (supports(tg, 'headerColor')) {
      tg.setHeaderColor(jungle)
      tg.setBackgroundColor(jungle)
    }
    if (supports(tg, 'bottomBarColor')) tg.setBottomBarColor(jungle)
  } catch {
    // Older clients reject custom colours; the default chrome is fine.
  }
}
