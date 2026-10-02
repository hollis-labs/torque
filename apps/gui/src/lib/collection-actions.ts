/** Apply only explicit IDs, with four workers and no new work after a failure. */
export async function runCollectionActions(ids: string[], action: (id: string) => Promise<void>) {
  const succeeded: string[] = []
  const failed: { id: string; message: string }[] = []
  let next = 0
  let stopped = false
  async function worker() {
    while (!stopped && next < ids.length) {
      const id = ids[next++]
      try { await action(id); succeeded.push(id) }
      catch (error) {
        stopped = true
        failed.push({ id, message: error instanceof Error ? error.message : String(error) })
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, ids.length) }, worker))
  return { succeeded, failed, notStarted: ids.slice(next) }
}
