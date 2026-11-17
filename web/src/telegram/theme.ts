import { supports, type WebApp } from './webapp'

/** Reads a colour token from src/index.css, e.g. --paper → "#f2efe6". */
function token(root: HTMLElement, name: string, fallback: string) {
  return getComputedStyle(root).getPropertyValue(name).trim() || fallback
}

/**
 * Follows Telegram's light/dark scheme and paints the native chrome with the
 * Field Manual paper colour of the active theme. themeParams colours are not
 * mapped onto the palette (docs/DECISIONS.md, Web).
 */
export function applyTelegramTheme(tg: WebApp, root: HTMLElement = document.documentElement) {
  root.classList.toggle('dark', tg.colorScheme === 'dark')
  root.style.colorScheme = tg.colorScheme

  const dark = tg.colorScheme === 'dark'
  const paper = token(root, '--paper', dark ? '#14170f' : '#f2efe6')
  try {
    if (supports(tg, 'headerColor')) {
      tg.setHeaderColor(paper)
      tg.setBackgroundColor(paper)
    }
    if (supports(tg, 'bottomBarColor')) tg.setBottomBarColor(paper)
  } catch {
    // Older clients reject custom colours; the default chrome is fine.
  }
}
