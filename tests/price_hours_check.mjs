// node tests/price_hours_check.mjs: the hour-of-day averages of the price watch graph
import assert from 'node:assert/strict'
import { averageByHour } from '../ui/src/utils/price-hours.js'

const at = (h, m = 0) => new Date(2026, 10, 18, h, m).toISOString()
// 1.80 from 06:00, 1.70 from 06:30, 1.90 from 08:00 until 09:00
const avg = averageByHour([{ at: at(6), price: 1.8 }, { at: at(6, 30), price: 1.7 }, { at: at(8), price: 1.9 }], new Date(2026, 10, 18, 9).getTime())
assert.ok(Math.abs(avg[6] - 1.75) < 1e-9, 'half an hour each') // weighted by time
assert.equal(avg[7], 1.7) // the price holds until the next change
assert.equal(avg[8], 1.9) // the last price holds until the end
assert.equal(avg[5], null)
assert.equal(avg[9], null)
assert.deepEqual(averageByHour([]), new Array(24).fill(null))
console.log('price hours ok')
