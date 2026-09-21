import { h } from 'vue'
import { toast } from 'vue-sonner'

// Green check for success, red X for failure. Sonner renders its own monochrome
// glyphs (they inherit the toast's text colour), so both are passed through the
// `icon` option to keep one icon style for every toast in the app — the icons'
// own colour is deliberate and independent of the accent tint.
//
// currentColor would leave the glyph the toast's text colour, which is exactly
// what we are replacing, so each icon carries an explicit hue that stays legible
// on both the light and the dark tinted card.
const SUCCESS_GREEN = '#16a34a' // green-600
const ERROR_RED = '#dc2626' // red-600

function CheckIcon() {
  return h(
    'svg',
    {
      viewBox: '0 0 24 24',
      width: 16,
      height: 16,
      fill: 'none',
      stroke: SUCCESS_GREEN,
      'stroke-width': 3,
      'stroke-linecap': 'round',
      'stroke-linejoin': 'round',
      'aria-hidden': 'true',
      'data-toast-icon': 'success',
    },
    [h('path', { d: 'M20 6 9 17l-5-5' })],
  )
}

function CrossIcon() {
  return h(
    'svg',
    {
      viewBox: '0 0 24 24',
      width: 16,
      height: 16,
      fill: 'none',
      stroke: ERROR_RED,
      'stroke-width': 3,
      'stroke-linecap': 'round',
      'stroke-linejoin': 'round',
      'aria-hidden': 'true',
      'data-toast-icon': 'error',
    },
    [h('path', { d: 'M18 6 6 18M6 6l12 12' })],
  )
}

/**
 * Success toast, always with the green check. Every successful user action that
 * reports back goes through here so success and failure stay symmetrical and no
 * call site can forget the icon.
 */
export function toastSuccess(msg: string, description?: string) {
  toast.success(msg, { icon: CheckIcon, description })
}

/**
 * Failure toast, always with the red X. Shows `fallbackMsg` (or a generic
 * message) and, when the error carries a server message, that message as the
 * description.
 *
 * The server message is read structurally rather than with `instanceof ApiError`.
 * Importing ApiError here makes this module depend on the api module just to
 * render a toast, and vitest THROWS when a partially-mocked api module is probed
 * for an export the mock omits ("No ApiError export is defined on the mock").
 * That throw lands inside the caller's catch block, swallowing the original error
 * and skipping its cleanup — which is exactly how a toast helper breaks a feature.
 * Every error the api layer raises is an Error, so `message` is the reliable
 * signal.
 */
export function toastError(e: unknown, fallbackMsg = 'Something went wrong') {
  toast.error(fallbackMsg, {
    icon: CrossIcon,
    description: serverMessage(e),
  })
}

function serverMessage(e: unknown): string | undefined {
  if (e && typeof e === 'object' && 'message' in e) {
    const msg = (e as { message?: unknown }).message
    if (typeof msg === 'string' && msg) return msg
  }
  return undefined
}
