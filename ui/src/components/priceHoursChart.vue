<script>
import { Bar } from 'vue-chartjs'
import { isDark } from '@utils/theme'
import { averageByHour } from '@utils/price-hours'

// "Cheapest time of day": average price per hour over the shown period, the cheapest hours green
export default {
  extends: Bar,
  props: { history: { type: Object, required: true } },
  watch: {
    history() {
      this.show()
    },
  },
  mounted() {
    this.show()
  },
  methods: {
    show() {
      const avg = averageByHour(this.history.prices)
      const known = avg.filter((v) => v !== null)
      const low = Math.min(...known)
      const high = Math.max(...known)
      const text = isDark() ? '#d6dbe1' : '#666'
      const grid = isDark() ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.1)'
      // cheapest = within 0.5 cent of the lowest hour
      const colors = avg.map((v) => (v !== null && v <= low + 0.005 ? '#39a275' : '#7a8a9a'))
      this.renderChart(
        {
          labels: avg.map((_, h) => String(h).padStart(2, '0')),
          datasets: [{ label: 'Average price by hour of day', backgroundColor: colors, data: avg }],
        },
        {
          maintainAspectRatio: false,
          animation: false,
          legend: { labels: { fontColor: text, boxWidth: 0 } },
          tooltips: { callbacks: { title: (i) => `${i[0].xLabel}:00–${i[0].xLabel}:59`, label: (i) => Number(i.yLabel).toFixed(3) } },
          scales: {
            xAxes: [{ ticks: { fontColor: text, maxRotation: 0, autoSkipPadding: 8 }, gridLines: { display: false } }],
            yAxes: [
              {
                // not from zero: the differences are cents
                ticks: { fontColor: text, min: Math.floor((low - 0.01) * 100) / 100, max: Math.ceil((high + 0.005) * 100) / 100, callback: (v) => v.toFixed(2) },
                gridLines: { color: grid, zeroLineColor: grid },
              },
            ],
          },
          plugins: { zoom: false },
        }
      )
    },
  },
}
</script>
