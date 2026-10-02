const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
import { fileURLToPath } from 'node:url'
import { writeFileSync } from 'node:fs'
const baseURL = process.env.GUI_BASE_URL ?? 'http://127.0.0.1:5218'
const dir = fileURLToPath(new URL('.', import.meta.url))
const requests = [], errors = []
const stamp = '2026-10-02T00:00:00Z'
const collections = Array.from({ length: 75 }, (_, i) => ({ id: `COL-${i + 1}`, name: i === 0 ? 'Engineering backlog' : `Collection ${i + 1}`, description: 'Server-searched collections', created_at: stamp, updated_at: stamp, archived_at: i === 74 ? stamp : null }))
let tasks = Array.from({ length: 301 }, (_, i) => ({ id: `T-${i}`, title: `Task ${i} production backlog`, status: i === 300 ? 'doing' : 'todo', priority: 2, collection_id: 'COL-1', created_at: stamp, updated_at: stamp, tags: [] }))
let small = Array.from({ length: 8 }, (_, i) => ({ ...tasks[i], id: `B-${i}`, title: `Small task ${i}`, collection_id: 'COL-2' }))
const page = (items, query) => {
  const at = Number(query.cursor ?? 0), limit = Number(query.limit ?? 50), batch = items.slice(at, at + limit), more = at + limit < items.length
  return { items: batch, meta: { returned: batch.length, limit, total: items.length, has_more: more, next_cursor: more ? String(at + limit) : null } }
}
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const p = await context.newPage()
p.on('pageerror', error => errors.push(error.stack))
await p.route('**/api/v1/**', async route => {
  const url = new URL(route.request().url()), path = url.pathname.replace('/api/v1', ''), query = Object.fromEntries(url.searchParams), method = route.request().method()
  requests.push({ path, query, method })
  let data = {}, status = 200
  if (path === '/features') data = { collections: true, projects: true, sprints: true, epics: true }
  else if (path === '/collections') data = page(collections.filter(c => (!query.search || c.name.toLowerCase().includes(query.search.toLowerCase())) && (query.status === 'all' || query.status === 'archived' ? query.status === 'all' || c.archived_at : !c.archived_at)), query)
  else if (path === '/collections/inbox/tasks') data = page([], query)
  else if (method === 'DELETE' && path.startsWith('/collections/COL-2/tasks/')) {
    const id = path.split('/').at(-1)
    if (id === 'B-0') { status = 409; data = { error: 'membership changed' } }
    else { await new Promise(resolve => setTimeout(resolve, 150)); small = small.filter(t => t.id !== id) }
  }
  else if (/^\/collections\/[^/]+\/tasks$/.test(path)) {
    let cohort = path.includes('COL-1/') ? tasks : path.includes('COL-2/') ? small : []
    cohort = cohort.filter(t => (!query.search || t.title.toLowerCase().includes(query.search.toLowerCase())) && (!query.status || t.status === query.status))
    data = page(cohort, query)
  }
  else if (method === 'POST' && /^\/tasks\/[^/]+\/transition$/.test(path)) {
    const id = path.split('/')[2], target = route.request().postDataJSON().status
    tasks = tasks.map(t => t.id === id ? { ...t, status: target } : t)
    data = tasks.find(t => t.id === id)
  }
  else if (path === '/runs') data = page([], query)
  else if (path.includes('events') || path.includes('stream')) return route.fulfill({ status: 200, contentType: 'text/event-stream', body: ': fixture\n\n' })
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
})
const capture = name => p.screenshot({ path: `${dir}/${name}.png` })
try {
  await p.goto(`${baseURL}/collections`)
  await p.getByText('Task 49 production backlog', { exact: true }).waitFor()
  if (requests.some(r => r.query.cursor)) throw new Error('automatic cursor continuation')
  await capture('01-server-paged-collections')
  await p.getByRole('button', { name: 'Doing', exact: true }).click()
  await p.getByText('Task 300 production backlog', { exact: true }).waitFor()
  const searched = p.waitForResponse(response => { const url = new URL(response.url()); return url.pathname.endsWith('/collections/COL-1/tasks') && url.searchParams.get('status') === 'doing' && url.searchParams.get('search') === '300' })
  await p.getByRole('textbox', { name: 'Search collection tasks' }).fill('300')
  await searched
  await p.waitForFunction(() => document.querySelectorAll('[data-task-id="T-300"]').length === 1)
  await p.waitForTimeout(350)
  if (!requests.some(r => r.path === '/collections/COL-1/tasks' && r.query.status === 'doing' && r.query.search === '300')) throw new Error('filter stayed client-side')
  await capture('02-server-status-search')
  await p.getByRole('textbox', { name: 'Search collection tasks' }).fill('')
  await p.getByRole('button', { name: 'All statuses', exact: true }).click()
  await p.getByText('Task 0 production backlog', { exact: true }).waitFor()
  await p.getByRole('checkbox', { name: 'Select Task 0 production backlog', exact: true }).check()
  await p.getByRole('checkbox', { name: 'Select Task 1 production backlog', exact: true }).check()
  await p.getByRole('button', { name: 'Change selected status', exact: true }).click()
  await p.getByRole('menuitem', { name: 'Done', exact: true }).click()
  await p.getByText('Change selected status: 2 succeeded, 0 failed, 0 not attempted.', { exact: true }).waitFor()
  await capture('03-explicit-selection-status')
  await p.getByRole('button', { name: 'Actions for collection Collection 2', exact: true }).click()
  await p.getByRole('menuitem', { name: 'Clear collection tasks', exact: true }).click()
  await p.getByRole('heading', { name: 'Remove 8 tasks from "Collection 2"?', exact: true }).waitFor()
  await capture('04-clear-exact-count-confirmation')
  await p.getByRole('button', { name: 'Remove assignments', exact: true }).click()
  await p.getByRole('alert').filter({ hasText: 'Remove collection assignments: 3 removed, 1 failed, 4 not attempted.' }).waitFor()
  await p.getByText('B-0: membership changed', { exact: true }).waitFor()
  const deletes = requests.filter(r => r.method === 'DELETE')
  if (deletes.length !== 4) throw new Error(`clear did not stop scheduling (${deletes.length} calls)`)
  await capture('05-clear-partial-result')
  await p.getByRole('textbox', { name: 'Search collections', exact: true }).fill('Collection 70')
  await p.getByRole('button', { name: 'Actions for collection Collection 70', exact: true }).waitFor()
  await capture('06-server-collection-search')
  writeFileSync(`${dir}/network.json`, JSON.stringify({ fixture_sizes: { collections: 75, main_tasks: 301, clear_tasks: 8 }, requests, errors }, null, 2))
  if (errors.length) throw new Error(errors.join('\n'))
  console.log('Server-filter, explicit-selection and partial-clear browser assertions passed')
} catch (error) { console.log('Browser errors', errors, 'DELETE requests', requests.filter(r => r.method === 'DELETE'), 'Alerts', await p.getByRole('alert').allTextContents()); await p.screenshot({path: '/tmp/CW-0071-browser-debug.png'}); throw error } finally { await browser.close() }
