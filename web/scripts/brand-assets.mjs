// Regenerates the Jungle Monkey brand assets from docs/brand/jungle-monkey.png.
// Run: node web/scripts/brand-assets.mjs (from the repo root or web/).
import sharp from 'sharp'
import { mkdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const src = join(root, 'docs/brand/jungle-monkey.png')
const brand = join(root, 'docs/brand')
const icons = join(root, 'web/public/icons')
const pub = join(root, 'web/public/brand')
mkdirSync(icons, { recursive: true })
mkdirSync(pub, { recursive: true })

const PAPER = '#F2EFE6'
const INK = '#1B1F1A'
// Head and shoulders on the 1920 px source.
const HEAD = { left: 740, top: 170, width: 540, height: 540 }

const head = () => sharp(src).extract(HEAD)

/** The "stamp": the head crop inside a square ink frame (1.5 px at 32 px, scaled up). */
async function stamp(size, out) {
  const border = Math.max(1, Math.round((size / 32) * 1.5))
  const inner = size - border * 2
  const img = await head().resize(inner, inner).png().toBuffer()
  await sharp({ create: { width: size, height: size, channels: 4, background: INK } })
    .composite([{ input: img, left: border, top: border }])
    .png({ compressionLevel: 9, palette: true, quality: 90 })
    .toFile(out)
}

// Hero: the full illustration.
await sharp(src).resize(1200, 1200).webp({ quality: 74 }).toFile(join(brand, 'jungle-monkey-hero.webp'))
await sharp(src).resize(960, 960).png({ compressionLevel: 9, palette: true, quality: 85 }).toFile(join(brand, 'jungle-monkey-hero.png'))
await sharp(src).resize(800, 800).webp({ quality: 72 }).toFile(join(pub, 'hero.webp'))

// Square head crops.
for (const size of [512, 192, 180, 32, 16]) {
  await head().resize(size, size).png({ compressionLevel: 9, palette: true, quality: 90 }).toFile(join(brand, `jungle-monkey-icon-${size}.png`))
}
// Maskable: the head inside the 80 % safe zone on paper.
const safe = Math.round(512 * 0.72)
const maskable = await sharp({ create: { width: 512, height: 512, channels: 4, background: PAPER } })
  .composite([{ input: await head().resize(safe, safe).png().toBuffer(), left: (512 - safe) >> 1, top: (512 - safe) >> 1 }])
  .png({ compressionLevel: 9, palette: true, quality: 90 })
  .toBuffer()
await sharp(maskable).toFile(join(brand, 'jungle-monkey-icon-maskable-512.png'))
await sharp(maskable).toFile(join(icons, 'maskable-512.png'))

// Stamps: app icons, favicons, header mark, Telegram bot avatar suggestion.
await stamp(512, join(icons, 'icon-512.png'))
await stamp(192, join(icons, 'icon-192.png'))
await stamp(180, join(icons, 'apple-touch-icon.png'))
await stamp(32, join(icons, 'favicon-32.png'))
await stamp(16, join(icons, 'favicon-16.png'))
await stamp(128, join(pub, 'stamp-128.png'))
await stamp(512, join(brand, 'jungle-monkey-stamp-512.png'))

// OpenGraph card: 1200x630, illustration on the left, wordmark on paper.
const ogArt = await sharp(src).resize(630, 630).png().toBuffer()
const wordmark = Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="570" height="630">
  <rect width="570" height="630" fill="${PAPER}"/>
  <rect x="0" y="0" width="3" height="630" fill="${INK}"/>
  <text x="48" y="200" font-family="IBM Plex Mono, DejaVu Sans Mono, monospace" font-size="20" letter-spacing="3" fill="#5E6357">AGENT ORCHESTRATOR · Nº 01</text>
  <text x="48" y="276" font-family="IBM Plex Sans, DejaVu Sans, sans-serif" font-weight="700" font-size="60" fill="${INK}">Jungle Monkey</text>
  <rect x="48" y="304" width="474" height="3" fill="${INK}"/>
  <text x="48" y="356" font-family="IBM Plex Sans, DejaVu Sans, sans-serif" font-weight="700" font-size="30" fill="${INK}">Every coding agent.</text>
  <text x="48" y="396" font-family="IBM Plex Sans, DejaVu Sans, sans-serif" font-weight="700" font-size="30" fill="${INK}">One control room.</text>
  <text x="48" y="440" font-family="IBM Plex Mono, DejaVu Sans Mono, monospace" font-size="18" fill="#3D4238">Claude Code · Codex · Cursor</text>
  <rect x="48" y="476" width="176" height="40" fill="#F06A1D" stroke="${INK}" stroke-width="3"/>
  <text x="62" y="503" font-family="IBM Plex Mono, DejaVu Sans Mono, monospace" font-weight="700" font-size="16" letter-spacing="2" fill="${INK}">● 3 RUNNING</text>
</svg>`)
await sharp({ create: { width: 1200, height: 630, channels: 4, background: PAPER } })
  .composite([{ input: ogArt, left: 0, top: 0 }, { input: wordmark, left: 630, top: 0 }])
  .jpeg({ quality: 82 })
  .toFile(join(brand, 'jungle-monkey-og.jpg'))
console.log('brand assets written')
