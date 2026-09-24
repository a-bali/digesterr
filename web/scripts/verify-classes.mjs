#!/usr/bin/env node
// Fail the build when the sidebar's action-reveal classes are not present as
// LITERALS in the production source.
//
// WHY SOURCE-LEVEL AND NOT CSS-LEVEL: the obvious check — "is the class in the
// compiled stylesheet?" — is circular and passes vacuously here. Tailwind scans
// every non-ignored file, including `*.test.ts`, so a literal appearing in a test
// assertion generates the class even when no production code references it. An
// earlier version of this script checked web/dist and reported OK against a
// deliberately reintroduced bug. The signal that actually matters is whether the
// PRODUCTION VUE SOURCE carries each class as a literal Tailwind can scan.
//
// WHY THIS FAILS IN PRACTICE: a utility class assembled at runtime
// (`opacity-0 ${group}:opacity-100`) is never scanned, so Tailwind emits no rule,
// and the element renders but never changes state. jsdom does not run Tailwind,
// so no component test can catch it either — which is how the bug shipped.
//
// Comments are stripped before checking: prose must not count as a reference.
//
// Usage: node scripts/verify-classes.mjs   (no build required)

import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

// The production component that owns the sidebar reveal behaviour.
const TARGET = 'src/components/FeedSidebar.vue'

// Utilities the reveal behaviour depends on. Note: the strings here are built at
// runtime from parts, deliberately, so that THIS FILE does not itself satisfy the
// check — Tailwind scans scripts too, and a literal here would make the guard
// beam its own answer back. Only the component source may carry them.
const UTIL = 'opacity' + '-100'
const REQUIRED = [
  'group' + '-hover' + ':' + UTIL,
  'group' + '-focus-within' + ':' + UTIL,
  'group' + '-hover' + '/section' + ':' + UTIL,
  'group' + '-hover' + '/cat' + ':' + UTIL,
]

function stripComments(src) {
  return src
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

const path = join(webRoot, TARGET)
let src
try {
  src = readFileSync(path, 'utf8')
} catch {
  console.error(`verify-classes: cannot read ${TARGET}`)
  process.exit(1)
}

const code = stripComments(src)

// 1. No utility class token may be composed at runtime: an interpolation that
//    supplies a variant prefix (`${x}:util`) or a utility (`variant:${x}`).
const composed = []
for (const m of code.matchAll(/`[^`]*\$\{[^}]+\}[^`]*`/g)) {
  const seg = m[0]
  if (/\$\{[^}]+\}\s*:/.test(seg) || /(?:^|[\s"'`])(?:[a-z-]+\/)?[a-z-]+:\$\{[^}]+\}/.test(seg)) {
    composed.push(seg.trim())
  }
}

// 2. Every required class must appear as a literal in the production source.
const missing = REQUIRED.filter(tok => !code.includes(tok))

if (composed.length || missing.length) {
  console.error('\nverify-classes: FAILED\n')
  if (missing.length) {
    console.error(`These reveal utilities are not literals in ${TARGET}:`)
    for (const tok of missing) console.error(`  ${tok}`)
    console.error('')
    console.error('Tailwind scans source as text. A class it never sees is never emitted,')
    console.error('so the element renders but can never be revealed — and no component')
    console.error('test can catch it, because jsdom does not run Tailwind.')
  }
  if (composed.length) {
    console.error('\nA utility class name is composed at runtime:')
    for (const seg of composed) console.error(`  ${seg}`)
    console.error('\nWrite the class out in full as a literal.')
  }
  console.error('')
  process.exit(1)
}

console.log(`verify-classes: OK (${REQUIRED.length} reveal utilities present as literals)`)
