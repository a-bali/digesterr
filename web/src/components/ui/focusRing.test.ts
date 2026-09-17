import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Input from './Input.vue'
import Button from './Button.vue'

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
