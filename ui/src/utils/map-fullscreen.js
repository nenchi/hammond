import L from 'leaflet'

// A corner button that makes the map fill the screen (picking a spot on a phone) and back.
// ponytail: a fixed-position class over the page, not the Fullscreen API (iPhone Safari doesn't have it for divs).
// a class, not inline styles: Vue re-applies the map's own inline height when the page re-renders
// (e.g. new search results), so !important wins over it
let styled = false
function addStyle() {
  if (styled) return
  styled = true
  const css = document.createElement('style')
  css.textContent =
    '.map-full{position:fixed!important;top:0!important;left:0!important;right:0!important;bottom:0!important;' +
    'height:auto!important;margin:0!important;z-index:999!important;border-radius:0!important}'
  document.head.appendChild(css)
}

// returns exit(): back to the normal size (e.g. after picking a station in the fill-up form)
export function addFullscreenButton(map) {
  addStyle()
  let set = () => {}
  const Button = L.Control.extend({
    options: { position: 'topright' },
    onAdd() {
      const a = L.DomUtil.create('a', 'leaflet-bar map-fullscreen')
      a.href = '#'
      a.title = 'Full screen'
      a.setAttribute('role', 'button')
      a.textContent = '⤢'
      a.style.cssText = 'display:block;width:34px;height:34px;line-height:32px;text-align:center;font-size:20px;background:#fff;color:#333'
      set = (full) => {
        map.getContainer().classList.toggle('map-full', full)
        a.textContent = full ? '✕' : '⤢'
        a.title = full ? 'Close full screen' : 'Full screen'
        map.invalidateSize()
      }
      L.DomEvent.on(a, 'click', (e) => {
        L.DomEvent.stop(e)
        set(!map.getContainer().classList.contains('map-full'))
      })
      return a
    },
  })
  map.addControl(new Button())
  return () => set(false)
}
