// Screenshot capture for CW-20260417-0152 (Widget Groups B + C).
// Run with: node scripts/screenshot-CW-0152.mjs
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createRequire } from 'node:module'

// Playwright is not a package.json dep — resolve via the npx cache if present,
// else via node_modules. Use the require() fallback so `NODE_PATH` lookup works.
const req = createRequire(import.meta.url)
let chromium
try {
  chromium = req('playwright').chromium
} catch {
  const pwPath = process.env.PLAYWRIGHT_PATH
  if (!pwPath) {
    console.error('Set PLAYWRIGHT_PATH to the playwright package dir, or `npm install playwright`.')
    process.exit(1)
  }
  chromium = req(pwPath).chromium
}

const BASE = process.env.BASE || 'http://localhost:5182'
const OUT = join(process.cwd(), 'artifacts/CW-20260417-0152')

async function shoot(page, name) {
  await mkdir(OUT, { recursive: true })
  const path = join(OUT, `${name}.png`)
  await page.screenshot({ path, fullPage: false })
  console.log('TORQUE_ARTIFACT:', `apps/gui/artifacts/CW-20260417-0152/${name}.png`)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

// Dashboard — Activity tab (Group A, regression check).
await page.goto(`${BASE}/dashboard`)
await page.waitForSelector('[role="tablist"]', { timeout: 15_000 })
await page.waitForTimeout(1200)
await shoot(page, 'dashboard-activity')

// Dashboard — Mission Control tab (Group B).
await page.getByRole('tab', { name: 'Mission Control' }).click()
await page.waitForTimeout(1200)
await shoot(page, 'dashboard-mission-control')

// Dashboard — Usage tab (Group C).
await page.getByRole('tab', { name: 'Usage' }).click()
await page.waitForTimeout(1200)
await shoot(page, 'dashboard-usage')

// Widget preview page showing all 8 widgets for reference.
await page.setViewportSize({ width: 1440, height: 2800 })
await page.goto(`${BASE}/dashboard/_widget-preview`)
await page.waitForSelector('h1', { timeout: 15_000 })
await page.waitForTimeout(1500)
await shoot(page, 'widget-preview-full')

await browser.close()
console.log('done')
