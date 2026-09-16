import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import FeedDialog from './FeedDialog.vue'
import type { Feed } from '../types/api'

// The instance's AgentMail inbox comes from /api/health, so it is mocked here
// rather than passed in: the dialog must never invent an inbox of its own.
const mockHealth = vi.fn()
const mockDiscover = vi.fn()

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn(),
    fetch: vi.fn(),
    markRead: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    discover: (...args: any[]) => mockDiscover(...args),
  },
  healthApi: { get: (...args: any[]) => mockHealth(...args) },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn(), summarize: vi.fn(), getComments: vi.fn().mockResolvedValue({ comments: [] }) },
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0 }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => 'test-token'),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

vi.mock('vue-sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

function makeFeed(overrides: Partial<Feed>): Feed {
  return {
    id: 1, user_id: 1, category: '', type: 'rss', name: 'Existing Feed',
    url: 'https://example.com/rss', config: '{}', min_score: 0,
    last_fetched_at: null, status: 'active', extract_content: 0,
    last_error: '', last_error_at: null, last_error_severity: '',
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function mountDialog(props: { feed?: Feed | null } = {}) {
  return mount(FeedDialog, {
    props: { feed: props.feed ?? null, 'onUpdate:open': vi.fn(), 'onSave': vi.fn(), 'onDelete': vi.fn() },
    global: { plugins: [createPinia()] },
    attachTo: document.body,
  })
}

// The dialog renders through a portal into document.body, so its fields are
// queried there rather than off the wrapper's own tree.
function typeSelect(): HTMLSelectElement {
  return document.body.querySelector('select') as HTMLSelectElement
}

function emailSuffixInput(): HTMLInputElement {
  return document.body.querySelector('[data-email-suffix]') as HTMLInputElement
}

function text(selector: string): string {
  return (document.body.querySelector(selector)?.textContent ?? '').trim()
}

async function selectEmailType() {
  const sel = typeSelect()
  sel.value = 'email'
  sel.dispatchEvent(new Event('change'))
  await flushPromises()
  await flushPromises()
}

async function setSuffix(value: string) {
  const input = emailSuffixInput()
  input.value = value
  input.dispatchEvent(new Event('input'))
  await flushPromises()
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  document.body.innerHTML = ''
  mockHealth.mockResolvedValue({
    status: 'ok',
    email_enabled: true,
    email_inbox: 'name@agentmail.to',
    email_prefix: 'name+',
    email_domain: '@agentmail.to',
  })
})

describe('FeedDialog email feed type', () => {
  it('offers the email type', async () => {
    mountDialog()
    await flushPromises()
    const options = Array.from(typeSelect().options).map(o => o.value)
    expect(options).toContain('email')
  })

  // The prefix and domain are server state, so they must be shown as fixed
  // context and NOT be editable — only the suffix is the user's to choose.
  it('renders the fixed prefix and domain around an editable suffix', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()

    expect(text('[data-email-prefix]')).toBe('name+')
    expect(text('[data-email-domain]')).toBe('@agentmail.to')

    const suffix = emailSuffixInput()
    expect(suffix).toBeTruthy()
    expect(suffix.value).toBe('')

    // There must be no field holding the whole address: the only editable input
    // in the email branch is the suffix.
    const emailFields = document.body.querySelector('[data-email-fields]')!
    const inputs = Array.from(emailFields.querySelectorAll('input'))
    expect(inputs).toHaveLength(1)
    expect(inputs[0].hasAttribute('data-email-suffix')).toBe(true)
  })

  // The resolved address is the string the user hands to the publisher, and
  // with no catch-all on AgentMail a wrong one receives nothing at all — so it
  // must be visible, not merely implied.
  it('shows the resolved full address as the suffix is typed', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()
    await setSuffix('sport')

    expect(text('[data-email-resolved]')).toBe('name+sport@agentmail.to')
  })

  it('normalises an uppercase suffix into the resolved address', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()
    await setSuffix('Sport')

    expect(text('[data-email-resolved]')).toBe('name+sport@agentmail.to')
  })

  it('shows a validation message and no address for an invalid suffix', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()
    await setSuffix('bad suffix')

    expect(text('[data-email-resolved]')).toBe('')
    expect(document.body.textContent).toContain('letters, digits, dots')
  })

  it('prefills the name from the suffix without a discover round trip', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()
    await setSuffix('ai-weekly')

    // prefillName is bound to the suffix field's blur, which is what a user
    // leaving the field triggers.
    emailSuffixInput().dispatchEvent(new Event('blur'))
    await flushPromises()

    const nameInput = document.body.querySelector('input[placeholder="Feed name"]') as HTMLInputElement
    expect(nameInput.value).toBe('Ai Weekly')
    // Discovery for an email feed is local: the server has no endpoint that
    // could name an address, so a round trip would only risk a timeout.
    expect(mockDiscover).not.toHaveBeenCalled()
  })

  // Editing must prefill ONLY the suffix, or a round trip through the field
  // could corrupt the stored address.
  it('prefills only the suffix when editing an existing email feed', async () => {
    mountDialog({ feed: makeFeed({ type: 'email', name: 'Sport', url: 'name+sport@agentmail.to' }) })
    await flushPromises()

    expect(emailSuffixInput().value).toBe('sport')
    expect(text('[data-email-prefix]')).toBe('name+')
    expect(text('[data-email-domain]')).toBe('@agentmail.to')
  })

  it('disables the email option when the server has no AgentMail config', async () => {
    mockHealth.mockResolvedValue({ status: 'ok', email_enabled: false })
    mountDialog()
    await flushPromises()

    const emailOption = Array.from(typeSelect().options).find(o => o.value === 'email')!
    expect(emailOption.disabled).toBe(true)
    expect(emailOption.textContent).toContain('not configured')
  })

  it('does not offer auto-extraction for email feeds', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()

    const checkbox = document.body.querySelector('input[type="checkbox"][class*="toggle"]') as HTMLInputElement
    expect(checkbox.disabled).toBe(true)
  })

  it('fetches the inbox from health once, not on every type change', async () => {
    mountDialog()
    await flushPromises()
    await selectEmailType()

    expect(mockHealth).toHaveBeenCalledTimes(1)
  })

  // Regression: the email option must be selectable on open, with no prior type
  // change. The config used to be fetched from a watcher on form.type, but the
  // option is DISABLED until the config is known — so a real user could never
  // trigger the fetch and the option stayed "not configured" forever. These
  // tests deliberately never call selectEmailType(): they open the dialog and
  // look straight at the option, which is what the user does.
  it('enables the email option on open, without any prior type change', async () => {
    mountDialog()
    await flushPromises()

    const emailOption = Array.from(typeSelect().options).find(o => o.value === 'email')!
    expect(emailOption.disabled).toBe(false)
    expect(emailOption.textContent).not.toContain('not configured')
  })

  it('requests the inbox on open rather than waiting for the type to change', async () => {
    mountDialog()
    await flushPromises()

    expect(mockHealth).toHaveBeenCalled()
  })

  // While /api/health is still in flight the inbox is unknown; rendering that as
  // "not configured" asserts something we have not established yet.
  it('does not claim "not configured" while the inbox request is in flight', async () => {
    let resolveHealth: (v: unknown) => void = () => {}
    mockHealth.mockReturnValue(new Promise(r => { resolveHealth = r }))

    mountDialog()
    await flushPromises()

    const pending = Array.from(typeSelect().options).find(o => o.value === 'email')!
    expect(pending.disabled).toBe(false)
    expect(pending.textContent).not.toContain('not configured')

    resolveHealth({ status: 'ok', email_enabled: true, email_inbox: 'name@agentmail.to' })
    await flushPromises()

    const settled = Array.from(typeSelect().options).find(o => o.value === 'email')!
    expect(settled.disabled).toBe(false)
  })

  // An edit form opens with the email type already set, so it needs the prefix
  // and domain on first render — not after a change the user cannot make.
  it('renders the fixed context when editing an existing email feed', async () => {
    mountDialog({ feed: makeFeed({ type: 'email', url: 'name+sport@agentmail.to' }) })
    await flushPromises()

    expect(text('[data-email-prefix]')).toBe('name+')
    expect(text('[data-email-domain]')).toBe('@agentmail.to')
    expect(emailSuffixInput().value).toBe('sport')
  })
})
