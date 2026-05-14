// Screenshot capture for CW-20260417-0086 bundle.
// Run with: node scripts/screenshot-bundle.mjs
import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

const BASE = process.env.BASE || 'http://localhost:5175'
const API  = process.env.API  || 'http://localhost:8990'
const OUT  = join(
  process.cwd(),
  '../../ui/artifacts/CW-20260417-0086',
)

async function pickTask() {
  const r = await fetch(`${API}/api/v1/tasks?limit=50`)
  const { tasks } = await r.json()
  // Prefer a doing task so the header/activity zone has data.
  const doing = tasks.find((t) => t.status === 'doing')
  return (doing ?? tasks[0]).id
}

async function shoot(page, name) {
  await mkdir(OUT, { recursive: true })
  const path = join(OUT, `${name}.png`)
  await page.screenshot({ path, fullPage: false })
  console.log('TORQUE_ARTIFACT:', `ui/artifacts/CW-20260417-0086/${name}.png`)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })

// 1. Tasks list with new Usage column + subtodos badge slot.
await page.goto(`${BASE}/`)
await page.waitForSelector('table', { timeout: 10_000 })
await page.waitForTimeout(500)
await shoot(page, '01-tasks-list')

// 2. Task detail — Details tab (new layout).
const taskId = await pickTask()
await page.goto(`${BASE}/tasks/${taskId}`)
await page.waitForSelector('h1', { timeout: 10_000 })
await page.waitForTimeout(500)
await shoot(page, '02-detail-details-tab')

// 3. Logs tab — activity timeline.
await page.getByRole('tab', { name: 'Logs' }).click()
await page.waitForTimeout(800)
await shoot(page, '03-detail-logs-tab')

// 4. Sub-todos tab.
await page.getByRole('tab', { name: 'Sub-todos' }).click()
await page.waitForTimeout(400)
await shoot(page, '04-detail-subtodos-tab')

// 5. Debug tab stub.
await page.getByRole('tab', { name: 'Debug' }).click()
await page.waitForTimeout(400)
await shoot(page, '05-detail-debug-tab')

// 6. Comments tab just to confirm tab strip.
await page.getByRole('tab', { name: 'Comments' }).click()
await page.waitForTimeout(400)
await shoot(page, '06-detail-comments-tab')

await browser.close()
console.log('done')
