# React + TypeScript + Vite

This template provides a minimal setup to get React working in Vite with HMR and some ESLint rules.

Currently, two official plugins are available:

- [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react) uses [Oxc](https://oxc.rs)
- [@vitejs/plugin-react-swc](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react-swc) uses [SWC](https://swc.rs/)

## React Compiler

The React Compiler is not enabled on this template because of its impact on dev & build performances. To add it, see [this documentation](https://react.dev/learn/react-compiler/installation).

## Expanding the ESLint configuration

If you are developing a production application, we recommend updating the configuration to enable type-aware lint rules:

```js
export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      // Other configs...

      // Remove tseslint.configs.recommended and replace with this
      tseslint.configs.recommendedTypeChecked,
      // Alternatively, use this for stricter rules
      tseslint.configs.strictTypeChecked,
      // Optionally, add this for stylistic rules
      tseslint.configs.stylisticTypeChecked,

      // Other configs...
    ],
    languageOptions: {
      parserOptions: {
        project: ['./tsconfig.node.json', './tsconfig.app.json'],
        tsconfigRootDir: import.meta.dirname,
      },
      // other options...
    },
  },
])
```

You can also install [eslint-plugin-react-x](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-x) and [eslint-plugin-react-dom](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-dom) for React-specific lint rules:

```js
// eslint.config.js
import reactX from 'eslint-plugin-react-x'
import reactDom from 'eslint-plugin-react-dom'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      // Other configs...
      // Enable lint rules for React
      reactX.configs['recommended-typescript'],
      // Enable lint rules for React DOM
      reactDom.configs.recommended,
    ],
    languageOptions: {
      parserOptions: {
        project: ['./tsconfig.node.json', './tsconfig.app.json'],
        tsconfigRootDir: import.meta.dirname,
      },
      // other options...
    },
  },
])
```

### Bounded lists (`usePagedList`)

`src/hooks/use-paged-list.ts` is transport agnostic. Its `fetchPage` adapter
returns one `{ items, meta }` page. It never exhausts the collection. For tasks:

```tsx
const fetchPage = useCallback(({ params, cursor, signal }: PageRequest<TaskParams>) =>
  api.listTaskSummaryPage({ ...params, cursor }, signal), [api])
const list = usePagedList({
  fetchPage,
  params: { project_id, sort_by: 'updated_at', sort_dir: 'desc', limit: 50 },
  getId: (task: TaskSummary) => task.id,
})
// TaskParams = Omit<TaskFilter, 'cursor' | 'offset'>
// Render list.items; an IntersectionObserver/button calls list.loadMore().
```

Query, sort, or filter changes reset to one page, abort previous requests, and
ignore responses from earlier generations even when a transport ignores abort.
Equivalent JSON params do not reset; do not put cursor/offset in `params`.
`queryKey` resets the list when its resource/transport changes (for example,
comments belonging to another entity). `enabled: false` pauses initial loading.
`loading`, `loadingMore`, `error`, `hasMore`, and `total` are exposed; `total` is
optional. Pass `include_total: true` explicitly when the UI requires it.
`reload()` / `refresh()` resets to the first page, rather than fetching every
previously viewed page.

Cursor mode is the default. Jump-to-page interfaces may set `mode: 'offset'`
and `initialOffset`; the first request sends `offset: 0` explicitly when no
other starting offset is specified. An offset adapter must forward `offset`
(not `cursor`), and the server must return `meta.next_offset`. Changing
`initialOffset` performs a jump. Tasks and runs support offset mode; other
resources currently use cursors. Never combine cursor and offset.

`TorqueApiClient` provides `listTaskPage`, `listTaskSummaryPage`, `pageRuns`,
`listProjects`, `listEpics`, `listSprints`, `listIssues`, and `listComments` as
single-page adapters accepting an optional final `AbortSignal`. Adapt arguments
with a stable `useCallback`. `listTasks` / `listTaskSummaries` also return exactly one page with `items`
and `meta`; no client API implicitly collects subsequent pages. Complete
exports are unavailable until a streaming export endpoint is implemented.

For SSE, either call `list.applyEvent({ id, patch, item, remove, refresh })` or
provide `subscribe(onChange) => unsubscribe` that converts transport events into
those changes. Only loaded IDs are patched/replaced/removed; unseen events only
call the debounced `onInvalidate` callback for counts/facets. A patch or fetched
row that fails `matches(row)` is removed. Do not put row-list refreshes inside
`onInvalidate`. Known field changes (such as task status transitions) need no
fetch. ID-only updates use optional `fetchItem(id, { params, signal })` with
`refresh: true`; return `null` for a removed row. Responses from old queries or
before newer direct patches are discarded.

Row refreshes deduplicate IDs, debounce for 200ms, and run with at most four
requests in flight. `rowRefreshDelayMs`, `rowRefreshConcurrency` (capped at four),
and `rowRefreshLimit` (default 20) configure this behavior. A burst exceeding the
loaded-ID limit stops row refreshes and sets `isStale`. Render a “New activity —
Refresh” action wired to `refresh()`. Failures to refresh a row also mark it
stale. Events never fetch the list. Patches preserve loaded order; new rows and
changed sort positions appear on explicit refresh.

Operations uses `usePagedList` in cursor mode with 50 task summaries per request. Status, priority, scope, tags, manual mode, internal visibility, debounced search and static eligibility are server query parameters; status cards use `/tasks/facets` with the identical cohort. Title / status / priority / date headers reset the cursor and request server order. Scroll loads one continuation page. Under search or eligibility, visible-row activity displays an inline refresh action; aggregate counts continue updating independently.

The filter editor stores named views in browser-local storage. Apply restores a snapshot, Reset restores the selected view (or defaults), and Save as / Save over explicitly persist it. Clear only resets working filters. Eligibility temporarily overrides the other filters and restores them when disabled; its tooltip distinguishes static eligibility from scheduler runtime checks. Review screenshots and an isolated 4,001-task network trace are in `artifacts/CW-20261001-0574/`.

Vitest uses one to two workers through `vitest.config.ts`, so the existing `npm run test:run` command has the same worker budget locally and in CI. The CPU-count default can oversubscribe shared hosts; prior runs intermittently timed out ScopeManagerDialog’s initial form and Collections’ continued-page renders. Assertion timeouts and behavior checks are unchanged; use explicit CLI worker overrides only when profiling concurrency.
