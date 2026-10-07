import { beforeEach, afterEach, describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import itemContentSource from '../components/ItemContent.vue?raw'
import digestContentSource from '../components/DigestContent.vue?raw'
import HtmlContent from '../components/HtmlContent.vue'
import { enhanceRedditVideos, redditVideoId } from './redditVideo'

const mock = vi.hoisted(() => ({ supported: true, instances: [] as Array<{ destroy: ReturnType<typeof vi.fn>; loadSource: ReturnType<typeof vi.fn>; attachMedia: ReturnType<typeof vi.fn>; error?: (event: string, data: { fatal: boolean }) => void }> }))
vi.mock('hls.js', () => ({ default: class {
  static isSupported() { return mock.supported }
  static Events = { ERROR: 'error' }
  destroy = vi.fn()
  loadSource = vi.fn()
  attachMedia = vi.fn()
  error?: (event: string, data: { fatal: boolean }) => void
  constructor(public config: { enableWorker: boolean }) { mock.instances.push(this) }
  on(_event: string, handler: (event: string, data: { fatal: boolean }) => void) { this.error = handler }
} }))
const asset = 'https://v.redd.it/2mu8k4a613nh1/CMAF_720.mp4?source=fallback'
const html = `<video src="${asset}" controls><a href="https://v.redd.it/2mu8k4a613nh1">Watch</a></video>`
function root(content = html) { const el = document.createElement('div'); el.innerHTML = content; return el }
beforeEach(() => {
  mock.instances.length = 0
  mock.supported = true
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
})
afterEach(() => vi.restoreAllMocks())

describe('Reddit audio playback routing', () => {
  it('attaches MSE HLS master to a stored MP4, exposes watch link and cleans up', () => {
    const el = root()
    const cleanup = enhanceRedditVideos(el)
    expect(mock.instances).toHaveLength(1)
    const instance = mock.instances[0]
    expect(instance).toHaveProperty('config.enableWorker', false)
    expect(instance.loadSource).toHaveBeenCalledWith('https://v.redd.it/2mu8k4a613nh1/HLSPlaylist.m3u8')
    expect(instance.attachMedia).toHaveBeenCalledWith(el.querySelector('video'))
    expect(el.querySelector('video')?.hasAttribute('src')).toBe(false)
    expect(el.querySelector('source')?.type).toBe('application/vnd.apple.mpegurl')
    expect(el.querySelectorAll('source')[1].src).toBe(asset)
    expect(el.querySelector('video a')).toBeNull()
    expect(el.querySelector('video')?.nextElementSibling?.getAttribute('href')).toContain('HLSPlaylist.m3u8')
    cleanup(); cleanup()
    expect(instance.destroy).toHaveBeenCalledTimes(1)
  })
  it('prefers native HLS without constructing hls.js', () => {
    vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('maybe')
    const el = root()
    const cleanup = enhanceRedditVideos(el)
    expect(mock.instances).toHaveLength(0)
    expect(el.querySelector('source')?.src).toContain('HLSPlaylist.m3u8')
    expect(HTMLMediaElement.prototype.load).toHaveBeenCalled()
    cleanup()
    expect(HTMLMediaElement.prototype.pause).toHaveBeenCalled()
  })
  it('recovers MSE when advertised native HLS actually selects silent MP4', () => {
    vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('maybe')
    const el = root()
    const cleanup = enhanceRedditVideos(el)
    expect(mock.instances).toHaveLength(0)
    const video = el.querySelector('video')!
    Object.defineProperty(video, 'currentSrc', { value: asset, configurable: true })
    video.dispatchEvent(new Event('loadedmetadata'))
    video.dispatchEvent(new Event('loadedmetadata'))
    expect(mock.instances).toHaveLength(1)
    cleanup()
    expect(mock.instances[0].destroy).toHaveBeenCalledTimes(1)
  })
  it('retains native HLS when the master really is selected', () => {
    vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('maybe')
    const el = root()
    const cleanup = enhanceRedditVideos(el)
    const video = el.querySelector('video')!
    Object.defineProperty(video, 'currentSrc', { value: 'https://v.redd.it/2mu8k4a613nh1/HLSPlaylist.m3u8' })
    video.dispatchEvent(new Event('loadedmetadata'))
    expect(mock.instances).toHaveLength(0)
    cleanup()
  })
  it('retains MP4 after unsupported HLS and without MSE', () => {
    mock.supported = false
    const el = root()
    enhanceRedditVideos(el)
    expect(mock.instances).toHaveLength(0)
    expect(el.querySelectorAll('source')[1].type).toBe('video/mp4')
    expect(el.querySelectorAll('source')[1].src).toBe(asset)
  })
  it('falls back to MP4 on fatal HLS error, not on nonfatal errors', () => {
    const el = root()
    const cleanup = enhanceRedditVideos(el)
    const instance = mock.instances[0]
    instance.error?.('error', { fatal: false })
    expect(instance.destroy).not.toHaveBeenCalled()
    instance.error?.('error', { fatal: true })
    expect(instance.destroy).toHaveBeenCalledTimes(1)
    expect(el.querySelector('source')?.type).toBe('video/mp4')
    cleanup()
    expect(instance.destroy).toHaveBeenCalledTimes(1)
  })
  it('does not touch external, Twitter, GIF, mixed-ID or malformed-host players', () => {
    for (const content of [
      '<video src="https://video.twimg.com/a.mp4"></video>',
      '<video src="https://example.com/a.mp4"></video>',
      html.replace(' controls', ' loop controls'),
      html.replace(' controls', ' data-reddit-gif controls'),
      html.replace('v.redd.it/', 'v.redd.it.evil.test/'),
      html.replace('</video>', '<source src="https://v.redd.it/other/CMAF_480.mp4"></video>'),
    ]) {
      const el = root(content)
      const before = el.innerHTML
      enhanceRedditVideos(el)
      expect(el.innerHTML).toBe(before)
    }
    expect(mock.instances).toHaveLength(0)
  })
  it('validates exact host, path and scheme', () => {
    for (const raw of ['https://v.redd.it@evil.test/id/CMAF_720.mp4', 'http://v.redd.it/id/CMAF_720.mp4', 'https://v.redd.it/id%2fother/CMAF_720.mp4', 'https://v.redd.it/id/CMAF_AUDIO_128.mp4', 'https://v.redd.it/id/CMAF_720.m3u8', 'https://v.redd.it:443/id/CMAF_720.mp4', 'https://v.redd.it/id/other/../CMAF_720.mp4']) expect(redditVideoId(raw)).toBeNull()
    expect(redditVideoId(asset)).toBe('2mu8k4a613nh1')
  })
  it('destroys old instance before content replacement and destroys new on unmount', async () => {
    const wrapper = mount(HtmlContent, { props: { html } })
    const old = mock.instances[0]
    await wrapper.setProps({ html: html.split('2mu8k4a613nh1').join('kprqyu3cd3th1') })
    expect(old.destroy).toHaveBeenCalledTimes(1)
    expect(mock.instances).toHaveLength(2)
    expect(mock.instances[1].loadSource).toHaveBeenCalledWith('https://v.redd.it/kprqyu3cd3th1/HLSPlaylist.m3u8')
    wrapper.unmount()
    expect(mock.instances[1].destroy).toHaveBeenCalledTimes(1)
  })
  it('routes every item, comment and digest HTML surface through the shared component', () => {
    for (const source of [itemContentSource, digestContentSource]) {
      expect(source).not.toContain('v-html=')
      expect(source).toContain('<HtmlContent')
    }
  })
})
