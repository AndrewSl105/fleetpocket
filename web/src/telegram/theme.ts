import { supports, type WebApp } from './webapp'

/** Reads a colour token from src/index.css, e.g. --background → "#ece9d8". */
function token(root: HTMLElement, name: string, fallback: string) {
  return getComputedStyle(root).getPropertyValue(name).trim() || fallback
}

/**
 * Follows Telegram's light/dark scheme and paints the native chrome to match.
 * The Windows XP palette in src/index.css is used as is: themeParams colours are
 * not mapped onto it (docs/DECISIONS.md, Web).
 */
export function applyTelegramTheme(tg: WebApp, root: HTMLElement = document.documentElement) {
  root.classList.toggle('dark', tg.colorScheme === 'dark')
  root.style.colorScheme = tg.colorScheme

  // The Telegram header becomes the window's title bar; the rest is the window face.
  const title = token(root, '--win-title-from', '#0058ee')
  const face = token(root, '--background', '#ece9d8')
  try {
    if (supports(tg, 'headerColor')) {
      tg.setHeaderColor(title)
      tg.setBackgroundColor(face)
    }
    if (supports(tg, 'bottomBarColor')) tg.setBottomBarColor(face)
  } catch {
    // Older clients reject custom colours; the default chrome is fine.
  }
}
