import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Button from './Button.vue'
import Input from './Input.vue'
import Skeleton from './Skeleton.vue'

describe('Button', () => {
  it('renders with default variant', () => {
    const wrapper = mount(Button, { slots: { default: 'Click' } })
    expect(wrapper.text()).toBe('Click')
    expect(wrapper.find('button').exists()).toBe(true)
  })

  it('applies variant classes', () => {
    const wrapper = mount(Button, { props: { variant: 'outline' }, slots: { default: 'Edit' } })
    expect(wrapper.classes()).toContain('border-border')
  })

  it('handles click events', () => {
    let clicked = false
    const wrapper = mount(Button, {
      slots: { default: 'Go' },
      attrs: { onClick: () => { clicked = true } },
    })
    wrapper.trigger('click')
    expect(clicked).toBe(true)
  })

  it('disables button', () => {
    const wrapper = mount(Button, { props: { disabled: true }, slots: { default: 'Disabled' } })
    expect(wrapper.find('button').attributes('disabled')).toBeDefined()
  })

  it('defaults to type="button" so it never submits an enclosing form', () => {
    const wrapper = mount(Button, { slots: { default: 'Delete' } })
    expect(wrapper.find('button').attributes('type')).toBe('button')
  })

  it('can opt in to type="submit"', () => {
    const wrapper = mount(Button, { props: { type: 'submit' }, slots: { default: 'Save' } })
    expect(wrapper.find('button').attributes('type')).toBe('submit')
  })
})

describe('Input', () => {
  it('renders an input element', () => {
    const wrapper = mount(Input)
    expect(wrapper.find('input').exists()).toBe(true)
  })

  it('accepts placeholder', () => {
    const wrapper = mount(Input, { attrs: { placeholder: 'Enter text' } })
    expect(wrapper.find('input').attributes('placeholder')).toBe('Enter text')
  })
})

describe('Skeleton', () => {
  it('renders a div with animation class', () => {
    const wrapper = mount(Skeleton)
    expect(wrapper.classes()).toContain('animate-pulse')
  })
})
