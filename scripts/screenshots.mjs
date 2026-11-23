// Screenshots for the README (images/) from a demo hammond filled by scripts/demo_data.py:
//   npm i playwright && npx playwright install chromium
//   node scripts/screenshots.mjs http://127.0.0.1:3000 <demo vehicle id> images
// The demo car photo: render scripts/demo_car.svg to a PNG (any browser: open it, screenshot) and pass it to
// demo_data.py. Emoji in the pages need an emoji font (e.g. Noto Color Emoji) on the machine.
// CHROME=<path> uses another Chromium binary.
import { chromium } from 'playwright'
const [base, vid, out] = process.argv.slice(2)
const browser = await chromium.launch(process.env.CHROME ? { executablePath: process.env.CHROME } : {})
const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 }, geolocation: { latitude: 50.1, longitude: 8.76 },
  permissions: ['geolocation'], deviceScaleFactor: 1 })
const page = await ctx.newPage()
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
await page.goto(base + '/login')
await page.locator('input').nth(0).fill('demo@example.com')
await page.locator('input[type=password]').fill('demo-password')
await page.locator('input[type=submit]').click()
await page.waitForURL((u) => !u.pathname.startsWith('/login'))
const shot = async (name, path, prep, opts = {}) => {
  await page.goto(base + path)
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(1200)
  if (prep) await prep()
  await page.waitForTimeout(opts.wait || 800)
  await page.screenshot({ path: `${out}/${name}.jpg`, type: 'jpeg', quality: 85, fullPage: !!opts.full })
  console.log('shot', name)
}
await shot('screenshot', '/')
await shot('vehicle_detail', `/vehicles/${vid}`, async () => {
  await page.locator('.stats-range select').selectOption({ label: 'This year' })
  await page.waitForTimeout(1500)
}, { full: true })
await shot('vehicle_edit', `/vehicles/${vid}/edit`, null, { full: true })
await shot('create_fillup', `/vehicles/${vid}/fillup`, async () => {
  await page.locator('.stations-nearby').click()
  await page.waitForTimeout(2500)
}, { full: true })
await shot('create_expense', `/vehicles/${vid}/expense`)
await shot('trips', `/vehicles/${vid}/trips`, null, { wait: 3500 })
await shot('price_watch', '/prices', async () => {
  const g = page.locator('button, a').filter({ hasText: /graph/i }).first()
  if (await g.count()) await g.click()
  await page.waitForTimeout(2500)
}, { full: true })
await shot('settings', '/settings', null, { full: true })
await shot('users', '/admin/users')
await shot('site_settings', '/admin/settings', null, { full: true })
// phone
await page.setViewportSize({ width: 390, height: 844 })
await shot('phone_home', '/')
await shot('phone_trips', `/vehicles/${vid}/trips`, async () => { await page.locator('.trips-map').scrollIntoViewIfNeeded(); await page.mouse.wheel(0, 120) }, { wait: 3500 })
await browser.close()
console.log('console errors:', errors.length ? errors.slice(0, 5) : 'none')
