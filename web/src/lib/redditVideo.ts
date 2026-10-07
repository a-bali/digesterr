import Hls from 'hls.js'

const assetPath = /^\/([A-Za-z0-9]+)\/(?:HLSPlaylist\.m3u8|(?:CMAF|DASH)_[0-9]+\.mp4)$/

export function redditVideoId(raw: string): string | null {
  if (!/^https:\/\/v\.redd\.it\/[A-Za-z0-9]+\/(?:HLSPlaylist\.m3u8|(?:CMAF|DASH)_[0-9]+\.mp4)(?:\?[^#]*)?$/.test(raw)) return null
  try {
    const u = new URL(raw)
    if (u.protocol !== 'https:' || u.host !== 'v.redd.it' || u.username || u.password || u.hash || u.pathname.includes('%')) return null
    return assetPath.exec(u.pathname)?.[1] ?? null
  } catch { return null }
}

/** Only CDN video assets are enhanced, not external/Twitter/looping GIF players.
 * DOM parsing is limited to the content container; stored HTML is never written.
 * Native HLS wins. MSE uses hls.js without a worker (no worker CSP exception).
 * Unsupported/fatal HLS falls back to progressive sources; the watch link stays.
 */
export function enhanceRedditVideos(root: HTMLElement): () => void {
  const cleanups: (() => void)[] = []
  for (const video of root.querySelectorAll('video')) {
    if (video.hasAttribute('loop') || video.hasAttribute('data-reddit-gif')) continue
    const sources = Array.from(video.querySelectorAll('source'))
    const urls = [video.getAttribute('src'), ...sources.map(s => s.getAttribute('src'))].filter((s): s is string => s !== null)
    const ids = urls.map(redditVideoId)
    const id = ids[0]
    if (!id || ids.some(other => other !== id)) continue
    const master = `https://v.redd.it/${id}/HLSPlaylist.m3u8`
    const fallback = urls.filter(url => new URL(url).pathname.endsWith('.mp4'))
    if (!fallback.length) fallback.push(`https://v.redd.it/${id}/CMAF_720.mp4`, `https://v.redd.it/${id}/CMAF_480.mp4`)
    video.removeAttribute('src')
    sources.forEach(s => s.remove())
    for (const [src, type] of [[master, 'application/vnd.apple.mpegurl'], ...fallback.map(url => [url, 'video/mp4'])]) {
      const source = document.createElement('source')
      source.src = src
      source.type = type
      video.append(source)
    }
    // Legacy fallback anchors inside video are invisible in supporting browsers.
    const inside = video.querySelector('a')
    if (inside) { inside.href = master; video.after(inside) }
    if (!(video.nextElementSibling instanceof HTMLAnchorElement)) {
      const link = document.createElement('a')
      link.href = master
      link.textContent = 'Watch video ↗'
      link.target = '_blank'
      link.rel = 'noopener noreferrer'
      video.after(link)
    }
    cleanups.push(() => {
      video.pause()
      video.removeAttribute('src')
      video.querySelectorAll('source').forEach(source => source.removeAttribute('src'))
      video.load()
    })
    let triedMSE = false
    function attachMSE() {
      if (triedMSE || !Hls.isSupported()) return
      triedMSE = true
      const hls = new Hls({ enableWorker: false })
      let destroyed = false
      const destroy = () => {
        if (!destroyed) { destroyed = true; hls.destroy() }
      }
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (!data.fatal) return
        destroy()
        video.removeAttribute('src')
        video.querySelector('source')?.remove()
        video.load()
      })
      hls.loadSource(master)
      hls.attachMedia(video)
      cleanups.push(destroy)
    }
    if (video.canPlayType('application/vnd.apple.mpegurl')) {
      // Some engines advertise HLS but silently select MP4 after failing the
      // master. Recover audio with MSE instead of accepting a silent fallback.
      const onMetadata = () => {
        if (redditVideoId(video.currentSrc) === id && /\.mp4(?:\?|$)/.test(video.currentSrc)) attachMSE()
      }
      video.addEventListener('loadedmetadata', onMetadata)
      cleanups.push(() => video.removeEventListener('loadedmetadata', onMetadata))
      video.load()
    } else if (Hls.isSupported()) {
      attachMSE()
    } else {
      video.load()
    }
  }
  return () => { for (const cleanup of cleanups.splice(0).reverse()) cleanup() }
}
