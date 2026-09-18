import { describe, it, expect, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import Input from './Input.vue'
import Button from './Button.vue'
import Dialog from './Dialog.vue'

// A focused control paints its focus ring OUTSIDE its border box (box-shadow
// spread), so a neighbour attached to its right edge covers the ring's right
// band unless the focused element is raised. Both elements were `position:
// static`, and the neighbour comes later in the DOM, so the neighbour won the
// paint order and the ring read as "missing on the right" — measured: the left
// edge showed a 2px ring band, the right edge only the neighbour's own border.
//
// jsdom does not paint, so these assert the two class-level causes that make the
// ring survive: the field is raised while focused, and `relative` is present
// because z-index is ignored on a static box. Delete the utility and the live
// regression is back, even though every other test still passes.
describe('Input focus ring vs an attached neighbour', () => {
  it('raises itself while focused so the ring is not painted over', () => {
    const wrapper = mount(Input)
    const cls = wrapper.find('input').classes()

    expect(cls).toContain('focus-visible:z-10')
    // z-index alone is inert on a static box — this pair is the fix.
    expect(cls).toContain('focus-visible:relative')
  })

  it('keeps the neighbour attached to the right edge', () => {
    const wrapper = mount({
      components: { Input, Button },
      template: `
        <div class="flex">
          <Input class="rounded-r-none" />
          <Button class="border-l-0">chevron</Button>
        </div>`,
    })

    const input = wrapper.find('input')
    const button = wrapper.find('button')

    // The split-button shape is what creates the overlap in the first place: the
    // input keeps a right border to hide, the neighbour drops its left one.
    expect(input.classes()).toContain('rounded-r-none')
    expect(button.classes()).toContain('border-l-0')
  })

  it('applies the same rule to Button, for split buttons whose prime half is a Button', () => {
    const wrapper = mount(Button, { slots: { default: 'Summarize' } })
    const cls = wrapper.find('button').classes()

    // The Summarize split button has this shape: its ring was covered by the
    // attached length-picker chevron, reachable by tabbing to it.
    expect(cls).toContain('focus-visible:z-10')
    expect(cls).toContain('focus-visible:relative')
  })
})

// The second way a ring disappears: a SCROLL CONTAINER clips at its padding box,
// so a control flush against the top of the scrollable dialog body loses the top
// band of its ring — the same "ring missing on one edge" symptom, a different
// cause. Measured live in the "Mark all as read?" dialog with its button focused:
// 2px bands at x=679/680 (left), x=762/763 (right) and y=518/519 (bottom), and
// nothing above y=482 at all. The fix is a zero-sum padding/negative-margin pair
// on the scroll body: it moves the clip edge (padding box) up by the 2px the ring
// needs while every child stays put (the button's own rect was unchanged at
// y=482), which matters because this body is shared by all nine dialogs.
describe('Dialog scroll body vs a focus ring at its top edge', () => {
  // Every mounted dialog teleports into document.body and stays there, so
  // without this the next test's `document.body.querySelector('[role="dialog"]')`
  // returns the PREVIOUS test's dialog — a stale-node read that fails on the
  // slot content rather than on the thing under test.
  beforeEach(() => { document.body.innerHTML = '' })

  function scrollBody(): HTMLElement {
    return document.body.querySelector('[role="dialog"] .overflow-y-auto') as HTMLElement
  }

  async function mountDialog(slot: string) {
    mount(Dialog, { props: { open: true, title: 'Mark all as read?' }, slots: { default: slot } })
    // The content lives in a reka-ui DialogPortal, which teleports into
    // document.body on the next tick rather than synchronously on mount.
    await flushPromises()
  }

  it('reserves the ring room on the scroll container itself', async () => {
    await mountDialog('<button>Mark read</button>')

    const cls = scrollBody().classList
    // The scrollport edge is what clips, so the classes must be on THAT element —
    // padding a wrapper around it would move the clip edge along with the child.
    expect(cls.contains('overflow-y-auto')).toBe(true)
    expect(cls.contains('pt-0.5')).toBe(true)
    expect(cls.contains('-mt-0.5')).toBe(true)
  })

  it('keeps the reserved space zero-sum so no dialog content shifts', async () => {
    await mountDialog('<button>Mark read</button>')

    // Read the numbers off the rendered element, not off a literal: the negative
    // margin must cancel the padding exactly, or every dialog's body gains or
    // loses height (and the shared component shifts content in nine dialogs).
    const cls = [...scrollBody().classList]
    const pad = cls.find(c => /^pt-/.test(c))
    const margin = cls.find(c => /^-mt-/.test(c))

    expect(pad).toBeDefined()
    expect(margin).toBeDefined()
    expect(margin!.replace('-mt-', '')).toBe(pad!.replace('pt-', ''))
  })

  it('still renders slot content inside the scrollable body', async () => {
    await mountDialog('<button data-mark-all-read-confirm>Mark read</button>')

    expect(scrollBody().querySelector('[data-mark-all-read-confirm]')).not.toBeNull()
  })
})
