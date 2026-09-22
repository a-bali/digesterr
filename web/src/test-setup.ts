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
