// Global jsdom gaps.
//
// reka-ui measures popper/dialog content with `new ResizeObserver(...)` inside
// onMounted (see reka-ui/dist/shared/useSize.js). jsdom does not provide
// ResizeObserver, so opening a menu/dialog triggers
// `ReferenceError: ResizeObserver is not defined` as an UNHANDLED REJECTION.
//
// That is the nastiest shape a test failure can take here: every test still
// passes, and vitest separately reports `Errors N errors` with a guess at which
// test caused it (the guess is wrong). It also fires intermittently, so a re-run
// "passing" proves nothing.
//
// Stubbing it once here replaces the hand-rolled copy that was pasted into every
// suite that happened to open a menu — a copy that each new test file could (and
// did) forget, which is how the intermittent error got in.
import { enableAutoUnmount } from '@vue/test-utils'
import { afterEach } from 'vitest'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

if (!('ResizeObserver' in globalThis)) {
  ;(globalThis as any).ResizeObserver = ResizeObserverStub
}

// jsdom does not implement matchMedia either; `useBreakpoints` (vueuse) calls it
// on mount. Individual suites that need to CONTROL the breakpoint still define
// their own query-aware stub — this only supplies a safe default so an unmocked
// mount cannot throw.
if (!('matchMedia' in globalThis)) {
  ;(globalThis as any).matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })
}

// Unmount every wrapper after each test.
//
// Without this, roughly half of the suite's `mount()` calls were never torn
// down. In a parallel worker the collection of a stale `HomeView` tree could
// land DURING a later test, unmounting reka-ui nodes that were teleported into
// `document.body` while the current test was still patching. That surfaced as
// unhandled rejections, and in two cases made a `document.body` lookup return a
// stale node instead of the live one:
//
//   TypeError: Cannot read properties of null (reading 'nextSibling')   ← removeFragment during unmount
//   TypeError: Cannot read properties of null (reading 'emitsOptions')  ← patch after the node was yanked
//
// Both are framework teardown, not test logic: every assertion passed and vitest
// still exited non-zero with `Errors N errors` — the same failure shape as the
// ResizeObserver gap above, and just as misleading.
//
// This is the only place teardown is configured. A suite that needs to inspect a
// wrapper after the test (e.g. the previous test's DOM) should unmount it
// explicitly instead of relying on the absence of this hook.
enableAutoUnmount(afterEach)
