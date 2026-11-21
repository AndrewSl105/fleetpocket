# Jungle Monkey — style guide

Read this before touching any page, component or copy. It replaces the old look: cream grid paper, tracked mono capitals, brutalist offset shadows. When something isn't covered here, choose the option that could only belong to this product, not the one every devtool site uses.

## 1. Idea

**Night rehearsal.** The monkey is the conductor and the coding agents are the orchestra. Everything happens on a dark jungle stage under warm amber light, just before the downbeat. It's calm and confident, with a little mischief.

Every screen should have one point of focus that is lit, while everything else stays in the dark.

## 2. Colour

| Token | Hex | Use |
|---|---|---|
| `--jungle` | `#14170F` | Page background. The brand colour (already the `theme-color`). |
| `--panel` | `#1F2618` | Raised surfaces: cards, code blocks, nav on scroll |
| `--line` | `#3A4530` | Hairline borders and dividers (decorative only, not for text) |
| `--amber` | `#F2B65A` | Stage light: focus rings, highlights, the "asking" state, links on hover |
| `--fur` | `#C8722F` | The accent: primary CTA fill, the "asks you" badge fill |
| `--fur-text` | `#E08A4E` | Fur-coloured **text** on dark (6.8:1) |
| `--bone` | `#EEE5D2` | Body and heading text (14.5:1) |
| `--muted` | `#9AA08A` | Secondary text (6.7:1 on jungle, 5.8:1 on panel) |

Rules:
- **Dark only.** There is no light theme and no cream. The page is a stage at night.
- **One accent per view.** Fur marks the single most important action or state on screen. Amber is light, not decoration: it falls *on* things (a glow, a pool, a focus ring) and isn't used as a flat fill.
- **Text on a fur fill is `--jungle`, never bone.** Bone on fur is 2.8:1 and fails contrast.
- Status colours come from the palette: running = `--amber` dot, asks you = `--fur` badge, done/PR = `--bone` with a check. No traffic-light greens or reds, and no blue.

## 3. Type

- **Display:** Gloock (Google Fonts), for headlines only. Weight 400, line-height 1.05, `text-wrap: balance`, sizes `clamp(40px, 6vw, 84px)` for the hero and `clamp(28px, 3.4vw, 44px)` for sections.
- **Text:** Figtree 400/600, 17px body with a 1.6 line-height and lines of 70 characters or fewer.
- **Mono:** JetBrains Mono, **only inside product UI** (task cards, logs, file paths, costs, commands). Never for labels, eyebrows, nav or decoration.
- Use sentence case everywhere. No letter-spaced capitals.
- Use four sizes per page at most. Nothing below 13px.

## 4. Imagery

- The hero art is the **painted monkey** (`docs/brand/jungle-monkey-hero.webp`). It's a painting with the jungle baked in, so don't cut it out, rotate limbs or repaint it. Fade its edges into `--jungle` with a soft gradient, and add an amber light pool so the painted sunlight reads as stage light.
- The monkey looks at the visitor. Its reaching arm points toward the product (the cards).
- Keep a marked slot for a future commissioned "monkey with baton" illustration. Until it exists, the painting stays.
- Photography, stock or 3D aren't used. Icons are simple 1.5px line icons in `--bone` or `--muted`. No emoji.

## 5. Components

- **Buttons:** 44px minimum height, 999px radius, Figtree 600 at 16px. Primary has a `--fur` fill with `--jungle` text. Secondary is transparent with a 1px `--line` border and `--bone` text. On hover, buttons lift with a soft amber glow (`box-shadow: 0 0 0 4px rgb(242 182 90 / .18)`). No hard offset shadows.
- **Cards (agent tasks):** `--panel` background, a 1px `--line` border, 14px radius. Header: task number and title in Figtree, agent name in `--muted`. Body is mono. The card that is asking gets an amber outer glow and a `--fur` "asks you" badge. There are no dashed borders.
- **Counters (READ / WEB / EDIT / SHELL / GIT):** mono labels in `--muted`, small square pips: read `--bone` 60%, web `--muted`, edit `--fur`, shell `--amber`, git `--bone`.
- **Code blocks:** `--panel` background with a Copy button at the top right. Comments in `--muted`.
- **Nav:** wordmark "Jungle Monkey" in Gloock with the small monkey stamp beside it, and links in Figtree 15px. It becomes `--panel` with a hairline bottom border once the page scrolls.
- **Sections:** separate them with space and light, not rules and numbers. No "§ 01", "Nº 01" or 01/02/03.

## 6. Motion

- There is one purposeful motion per view. In the hero, that's the demo: counters tick, then card #42 asks a question, an answer is chosen, and a card ends as "PR opened".
- A thin amber line from the monkey's hand to a card appears **only** while that card is asking.
- Easing: `cubic-bezier(.2,.7,.2,1)`, 200–400ms for UI and slower (1–2s) for light.
- Honour `prefers-reduced-motion`: show the final state with no animation.

## 7. Voice and copy

- The monkey speaks in first person: specific, a little cheeky, and never cute for its own sake. It talks about the actual job of wrangling several coding agents at once.
- Hero headline: **"Herding coding agents is my job. Yours is reading the PR."**
- The caption above the demo describes what the cards really show, for example "Claude has a question, Codex is halfway through DataTable.tsx, and nobody has touched your credit card." Keep it in sync with the cards.
- Sublines are one plain sentence about what the product does.
- Avoid three-part slogans, "Every X. One Y." formulas, lists joined by " · ", buzzwords ("seamless", "supercharge", "unleash") and exclamation marks.
- Buttons and labels use plain verbs: "Install", "GitHub", "Read the docs".

## 8. Layout

- Content width is at most 1240px. The background colour and light run edge to edge.
- Hero on desktop: the monkey takes the left ~45% and the headline sits top-left over the dark foliage. The cards hang on the right, slightly staggered.
- Under 600px, stack the headline, then the monkey, then the cards. Nothing sits side by side, and nothing may widen a 360px screen.
- Use generous vertical spacing (`clamp(64px, 9vw, 140px)` between sections).

## 9. Banned (the old defaults)

Cream or beige backgrounds · graph-paper grids · tracked uppercase mono eyebrows · Nº / § / numbered sections · brutalist offset shadows · dashed borders · tag rows separated by " · " · purple gradients · glassmorphism · glowing blobs · bento grids · emoji icons · Inter, Space Grotesk, Bricolage Grotesque or IBM Plex as the display face · "Every X. One Y." headlines

## 10. Done means

- [ ] All text passes WCAG AA. Check every colour pair you add.
- [ ] Mono appears only inside product UI.
- [ ] Each view has exactly one fur-coloured focal point.
- [ ] It works at 360px, 768px and 1440px.
- [ ] Reduced motion shows a complete static state.
- [ ] Nothing from section 9 remains anywhere on the site.
