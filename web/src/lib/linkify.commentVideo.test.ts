import { describe, it, expect } from 'vitest'
import { renderCommentHtml, commentVideoHtml, commentVideoMediaUrl } from './linkify'

/**
 * The reported defect (CLAIM): a comment whose body is an app-only Reddit
 * comment-video deep link rendered as a link to
 *
 *     https://reddit.com/link/<cid>/video/<mid>/player
 *
 * which Reddit redirects to "r/subreddit is banned" for an anonymous visitor.
 * The media id in that URL reaches the video directly, so the renderer emits an
 * inline player instead.
 *
 * These mirror `internal/linkify/comment_video_test.go`: the two
 * implementations are deliberate twins, so a construct handled in one and not
 * the other is a defect.
 *
 * Real input: r/opencode post 1wvyg3k, comment pdge9kv, media id
 * kprqyu3cd3th1 (author 2frames_app).
 */

const URL = 'https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player'

describe('renderCommentHtml — comment video', () => {
  it('renders an inline player instead of the banned-page link', () => {
    const got = renderCommentHtml(URL)

    expect(got).toContain('<video')
    expect(got).toContain('https://v.redd.it/kprqyu3cd3th1/CMAF_720.mp4')
    expect(got).toContain('https://v.redd.it/kprqyu3cd3th1/CMAF_480.mp4')
    expect(got).toContain('https://v.redd.it/kprqyu3cd3th1/HLSPlaylist.m3u8')

    // The defect itself: no link back to the banned page.
    expect(got).not.toContain('href="https://reddit.com/link/')
  })

  it('puts HLS first for audio-capable native readers and retains MP4 fallback', () => {
    const got = renderCommentHtml(URL)
    expect(got.indexOf('HLSPlaylist.m3u8')).toBeLessThan(got.indexOf('CMAF_720.mp4'))
    expect(got).toContain('</video><a href=')
  })

  it('never points a source at DASH, which is a 403 on this CDN', () => {
    expect(renderCommentHtml(URL)).not.toContain('DASH')
  })

  it('leaves a fallback anchor for a client that plays none of the sources', () => {
    expect(renderCommentHtml(URL)).toContain('Watch video')
  })

  it('accepts the shapes authors actually paste', () => {
    const variants = [
      'reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player',
      'made this horse: https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player enjoy',
      'https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player/',
      'https://www.reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player',
      'https://old.reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player',
      'https://m.reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player',
    ]
    for (const raw of variants) {
      expect(renderCommentHtml(raw), raw).toContain('<video')
    }
  })

  it('does not render a player for a mid-word match', () => {
    expect(renderCommentHtml('xhttps://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player')).not.toContain(
      '<video',
    )
  })

  it('leaves ordinary URLs as links', () => {
    const controls = [
      'https://example.com/a',
      'www.example.com/a',
      'https://www.reddit.com/r/opencode/comments/1wvyg3k/x/',
      'https://reddit.com/link/pdge9kv/comments',
      'https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1',
    ]
    for (const raw of controls) {
      const got = renderCommentHtml(raw)
      expect(got, raw).not.toContain('<video')
      expect(got, raw).toContain('<a href=')
    }
  })

  it('renders the player inside the block it sits in', () => {
    expect(renderCommentHtml('&gt; ' + URL)).toContain('<blockquote>')
    expect(renderCommentHtml('&gt; ' + URL)).toContain('<video')
    expect(renderCommentHtml('- ' + URL)).toContain('<li>')
    expect(renderCommentHtml('- ' + URL)).toContain('<video')
  })

  it('does not claim a URL that is already an href value', () => {
    const got = renderCommentHtml(`<a href="${URL}">clip</a>`)
    expect(got).not.toContain('<video')
    expect(got).toContain('clip')
  })

  it('does not nest a player inside a generated anchor', () => {
    const got = renderCommentHtml(`[x](${URL})`)
    expect(got.match(/<a href=/g)).toHaveLength(1)
    expect(got).not.toContain('<video')
  })
})

describe('commentVideo helpers', () => {
  it('rejects malformed IDs before interpolating HTML attributes', () => {
    for (const id of ['', '../other', 'id" onerror="alert(1)', 'id/extra']) expect(commentVideoHtml(id)).toBe('')
  })
  it('builds every URL from a fixed template, never from the input', () => {
    const html = commentVideoHtml('abc123XYZ')
    expect(html).toContain('<video controls')
    expect(html).toContain('https://v.redd.it/abc123XYZ/CMAF_720.mp4')
    expect(html).toContain('https://v.redd.it/abc123XYZ/HLSPlaylist.m3u8')
    expect(html).not.toContain('<script')
  })

  it('points the whole-thing URL at the HLS master, the only muxed asset', () => {
    expect(commentVideoMediaUrl('kprqyu3cd3th1')).toBe(
      'https://v.redd.it/kprqyu3cd3th1/HLSPlaylist.m3u8',
    )
  })
})
