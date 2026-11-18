// Chart.js 4 for all charts: every built-in chart type, the zoom/pan plugin (off unless a chart's
// options turn it on) and date-fns for time axes. ponytail: registerables instead of picking parts,
// a few kB more for one place to register.
import { Chart, registerables } from 'chart.js'
import zoomPlugin from 'chartjs-plugin-zoom'
import 'chartjs-adapter-date-fns'
import { isDark } from '@utils/theme'

Chart.register(...registerables, zoomPlugin)

// axis text and grid readable in both themes (Chart.js' defaults vanish on the dark theme)
export function themeColors() {
  return {
    text: isDark() ? '#d6dbe1' : '#666',
    grid: isDark() ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.1)',
  }
}

// the Chart.js instance inside a component's element (vue-chartjs 5 keeps it on its canvas)
export function chartIn(el) {
  const canvas = el && el.querySelector('canvas')
  return canvas ? Chart.getChart(canvas) : null
}
