// Browser verification for CW-20260418-0009 — deterministic row-click fix.
// Runs headless, navigates to the operations page, then programmatically
// dispatches clicks on: (a) the row body, (b) the checkbox, (c) the
// actions kebab trigger. Logs the URL after each click so we can confirm
// the row body deterministically navigates to /tasks/:id while the inner
// controls do not.
//
// Run with: BASE=http://localhost:5175 node scripts/verify-CW-0009.mjs
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createRequire } from 'node:module'

const req = createRequire(import.meta.url)
const { chromium } = req('playwright')

const BASE = process.env.BASE || 'http://localhost:5175'
const OUT = join(process.cwd(), 'artifacts/CW-20260418-0009')

async function shoot(page, name) {
  await mkdir(OUT, { recursive: true })
  const path = join(OUT, `${name}.png`)
  await page.screenshot({ path, fullPage: false })
  console.log('TORQUE_ARTIFACT: apps/gui/artifacts/CW-20260418-0009/' + name + '.png')
  return path
}

function log(label, value) {
  console.log(label.padEnd(32, ' '), value)
}

const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await context.newPage()

page.on('pageerror', (err) => console.log('PAGE ERROR:', err.message))

// Land on the ops page which renders TaskTable.
await page.goto(`${BASE}/operations?status=backlog,todo,queued,doing,review,blocked,done`)
await page.waitForSelector('[data-testid="task-row"]', { timeout: 20_000 })
await page.waitForTimeout(400)
await shoot(page, '01-before-list')

// Collect the first row and the inner controls for deterministic targeting.
const firstRow = page.locator('[data-testid="task-row"]').first()
const checkbox = firstRow.locator('[data-testid="task-row-checkbox"]')
const actions = firstRow.locator('[data-testid="task-row-actions-trigger"]')
const rowId = await firstRow.getAttribute('aria-label')
log('first row aria-label', rowId)

// ----- Test 1: checkbox click must NOT navigate. -----
const urlBefore1 = page.url()
await checkbox.click()
await page.waitForTimeout(150)
const urlAfter1 = page.url()
log('[checkbox] url unchanged', urlBefore1 === urlAfter1)
const checkboxChecked = await checkbox.isChecked()
log('[checkbox] state toggled on', checkboxChecked)

// ----- Test 2: kebab trigger click must NOT navigate. -----
const urlBefore2 = page.url()
await actions.click()
await page.waitForTimeout(150)
const urlAfter2 = page.url()
log('[kebab] url unchanged', urlBefore2 === urlAfter2)
const menuOpen = await page.locator('[role="menu"]').count()
log('[kebab] menu opened', menuOpen > 0)
// Close the menu by pressing Escape before clicking the row.
await page.keyboard.press('Escape')
await page.waitForTimeout(100)

// ----- Test 3: row body click MUST navigate. -----
// Click on the priority <td> — non-interactive, in the previous code the
// td did not stopPropagation, but the status td did. This aims at a
// deterministic non-interactive region.
const urlBefore3 = page.url()
const priCell = firstRow.locator('td').nth(3) // 0:checkbox, 1:title, 2:status, 3:priority
await priCell.click()
await page.waitForTimeout(400)
const urlAfter3 = page.url()
log('[row-body] url changed', urlBefore3 !== urlAfter3)
log('[row-body] new url', urlAfter3)
const navigatedToTask = /\/tasks\//.test(urlAfter3)
log('[row-body] navigated to /tasks/', navigatedToTask)
await shoot(page, '02-after-row-click')

// ----- Test 4 (regression): click the previously-broken status cell. -----
await page.goBack()
await page.waitForSelector('[data-testid="task-row"]', { timeout: 10_000 })
await page.waitForTimeout(300)
const row2 = page.locator('[data-testid="task-row"]').first()
const statusCell = row2.locator('td').nth(2)
const urlBefore4 = page.url()
await statusCell.click()
await page.waitForTimeout(400)
const urlAfter4 = page.url()
log('[status-cell] url changed', urlBefore4 !== urlAfter4)
log('[status-cell] navigated to /tasks/', /\/tasks\//.test(urlAfter4))

await browser.close()

// Collect pass/fail summary.
const results = {
  checkboxDidNotNavigate: urlBefore1 === urlAfter1,
  checkboxToggled: checkboxChecked,
  kebabDidNotNavigate: urlBefore2 === urlAfter2,
  kebabOpenedMenu: menuOpen > 0,
  rowBodyNavigated: urlBefore3 !== urlAfter3 && /\/tasks\//.test(urlAfter3),
  statusCellNavigated: urlBefore4 !== urlAfter4 && /\/tasks\//.test(urlAfter4),
}
console.log('\nSUMMARY', JSON.stringify(results, null, 2))
const allPass = Object.values(results).every(Boolean)
process.exit(allPass ? 0 : 1)
