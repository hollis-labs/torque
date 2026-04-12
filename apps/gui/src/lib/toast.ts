import { toast } from 'sonner'

// Single source of truth for unwrapping thrown values into user-facing text.
// When the API begins returning structured errors ({code, message, field},
// validation details, etc.), this is the one place that changes.
function extractMessage(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) return err.message
  if (typeof err === 'string' && err.length > 0) return err
  return fallback
}

// Call from catch blocks. Keeps the try/catch visible at the site so the
// site-specific state update flow stays readable.
export function notifyError(err: unknown, fallback: string): void {
  toast.error(extractMessage(err, fallback))
}

// Exported for future use. Errors-only policy in this pass means no call
// sites yet, but this is the idiomatic import path so future work never
// reaches for sonner directly.
export function notifySuccess(message: string): void {
  toast.success(message)
}
