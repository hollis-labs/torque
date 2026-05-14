// Uses playwright-core from the ambient cache (/tmp/node_modules/playwright-core)
// or any available copy. Adjust the import if playwright-core is installed
// locally in node_modules.
import pw from '/tmp/node_modules/playwright-core/index.js'
const { chromium } = pw
import path from 'node:path'
import fs from 'node:fs'

const ROOT = path.resolve(new URL('.', import.meta.url).pathname, '..')
const ARTIFACTS = path.resolve(ROOT, 'artifacts', 'CW-20260417-0151')
fs.mkdirSync(ARTIFACTS, { recursive: true })

const BASE = process.env.SCREENSHOT_BASE || 'http://localhost:5173'
const VP = { width: 1440, height: 900 }

async function shoot(page, url, filename) {
  await page.goto(url, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(2500)
  const out = path.join(ARTIFACTS, filename)
  await page.screenshot({ path: out, fullPage: false })
  console.log('TORQUE_ARTIFACT: apps/gui/artifacts/CW-20260417-0151/' + filename)
}

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: VP, colorScheme: 'dark' })
const page = await ctx.newPage()

await shoot(page, `${BASE}/dashboard`, '0151-dashboard-activity.png')
await shoot(page, `${BASE}/dashboard?tab=mission-control`, '0151-dashboard-mission-control.png')
await shoot(page, `${BASE}/dashboard?tab=usage`, '0151-dashboard-usage.png')
await shoot(page, `${BASE}/operations`, '0151-operations-compare.png')
await shoot(page, `${BASE}/dashboard/_widget-preview`, '0151-widget-preview.png')

await browser.close()
