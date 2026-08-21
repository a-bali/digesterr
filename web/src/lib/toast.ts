import { toast } from 'vue-sonner'
import { ApiError } from '../services/api'

// Shared error-toast helper. Shows `fallbackMsg` (or a generic message) and,
// when the error is an ApiError, its server-provided message as description.
export function toastError(e: unknown, fallbackMsg = 'Something went wrong') {
  toast.error(fallbackMsg, { description: e instanceof ApiError ? e.message : undefined })
}
