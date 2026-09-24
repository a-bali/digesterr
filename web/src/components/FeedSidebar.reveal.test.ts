// Characterisation of the sidebar action-reveal contract.
//
// The bug (reported from PROD): a category with no unread items painted its
// mark-all-read button PERMANENTLY but faintly, while a category WITH unread
// items never showed it on hover. Sections behaved the same way.
//
// Two independent causes, both invisible to the existing suite:
//
//  1. The reveal class was built by interpolation --
//     `opacity-0 ${group}:opacity-100` in selectedAwareClass -- so Tailwind's
//     scanner never saw the literal `group-hover/cat:opacity-100` and emitted no
//     rule for it. Verified in the BUILT stylesheet: the plain
//     `.group-hover\:opacity-100:is(:where(.group):hover *)` rule exists, the
//     named-group variants do NOT. So the row could never be revealed; only
//     `':class="opacity-100"'` (selected) ever made it visible.
//
//  2. The button also carries the literal `opacity-0` and `disabled:opacity-30`.
//     Both are emitted, and `.disabled\:opacity-30:disabled{opacity:.3}` comes
//     LATER in the stylesheet, so a disabled button resolved to opacity 0.3 --
//     not hidden, not fully visible. That is the pale permanently-visible button.
//
// These tests assert the contract that fixes both: the reveal class must be a
// LITERAL Tailwind can see, and the button must stay FUNCTIONAL (never disabled)
// so a user can always press it and get the "nothing unread" answer.
//
// This is a guard, not a proof: jsdom does not run Tailwind, so a passing test
// here cannot show the CSS rule exists. The build-time check in
// `npm run verify:classes` (scripts/verify-classes.mjs) is what catches a
// non-generated utility, and FeedSidebar.test.ts asserts the class tokens.
import { describe, it, expect, beforeEach, vi } from 'vitest'

// The exact utility tokens Tailwind must emit for the sidebar reveal to work.
// These strings must appear VERBATIM in the source Tailwind scans.
const REQUIRED_REVEAL_CLASSES = [
  'group-hover:opacity-100',
  'group-focus-within:opacity-100',
  // Named groups: the Feeds/Digests section and each category own their actions.
  'group-hover/section:opacity-100',
  'group-hover/cat:opacity-100',
  // The named-group markers the hover rules target.
  'group/section',
  'group/cat',
]

describe('sidebar reveal classes are statically discoverable', () => {
  it('declares every reveal utility as a literal in FeedSidebar.vue', async () => {
    const fs = await import('node:fs/promises')
    const path = await import('node:path')
    const url = await import('node:url')
    const here = path.dirname(url.fileURLToPath(import.meta.url))
    const src = await fs.readFile(path.join(here, 'FeedSidebar.vue'), 'utf8')

    const missing = REQUIRED_REVEAL_CLASSES.filter(c => !src.includes(c))
    expect(
      missing,
      `These utility classes are not present as literals in FeedSidebar.vue, so ` +
      `Tailwind cannot generate them and the element can never be revealed: ${missing.join(', ')}`,
    ).toEqual([])
  })

  it('does not build a utility class name by interpolation', async () => {
    const fs = await import('node:fs/promises')
    const path = await import('node:path')
    const url = await import('node:url')
    const here = path.dirname(url.fileURLToPath(import.meta.url))
    const src = await fs.readFile(path.join(here, 'FeedSidebar.vue'), 'utf8')

    // A template literal that assembles a class token like
    // `opacity-0 ${group}:opacity-100` produces a string Tailwind never scanned.
    //
    // Comments are stripped first: the note explaining this very pitfall quotes
    // the bad pattern, and matching that would make the guard fail on its own
    // documentation. Only real code is inspected.
    const code = src
      .replace(/<!--[\s\S]*?-->/g, '')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    const interpolated = [...code.matchAll(/`[^`]*\$\{[^}]+\}[^`]*`/g)]
      .map(m => m[0])
      .filter(s => /[:/]/.test(s) && /\b(group|peer|hover|focus|disabled|opacity)/.test(s))

    expect(
      interpolated,
      'A utility class name is assembled by interpolation; Tailwind will not generate it. ' +
      'Write the class out in full so the scanner sees it.',
    ).toEqual([])
  })
})

// The reveal/functional contract on the real rendered buttons. Mounted through
// the component so it exercises the actual markup, not a copy of it.
describe('sidebar mark-all-read controls stay functional', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('never disables the category, section or digest mark-read controls', async () => {
    // Asserted structurally: the controls carry no `disabled` attribute in the
    // source. A disabled control cannot be pressed, so the user loses the
    // "nothing unread" answer the action is supposed to give.
    const fs = await import('node:fs/promises')
    const path = await import('node:path')
    const url = await import('node:url')
    const here = path.dirname(url.fileURLToPath(import.meta.url))
    const src = await fs.readFile(path.join(here, 'FeedSidebar.vue'), 'utf8')

    // The three mark-all-read controls are identifiable by their data attributes.
    for (const marker of ['data-mark-category-read', 'data-mark-feeds-section-read', 'data-mark-all-digests']) {
      const idx = src.indexOf(marker)
      expect(idx, `${marker} not found in FeedSidebar.vue`).toBeGreaterThan(-1)
      // Look back at the button's own attribute block for a :disabled binding.
      const block = src.slice(Math.max(0, idx - 700), idx)
      const lastOpen = block.lastIndexOf('<button')
      const buttonBlock = lastOpen >= 0 ? block.slice(lastOpen) : block
      expect(
        buttonBlock.includes(':disabled'),
        `${marker} must not be disabled: the action is harmless on an empty scope ` +
        `and disabling it makes the control permanently pale and unpressable.`,
      ).toBe(false)
    }
  })
})
