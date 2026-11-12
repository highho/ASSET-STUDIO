import { ref } from 'vue'

export interface ToastItem {
  id: number
  message: string
  tone: 'info' | 'success' | 'error'
}

export const toasts = ref<ToastItem[]>([])

let sequence = 0

export function pushToast(message: string, tone: ToastItem['tone'] = 'info'): void {
  const id = (sequence += 1)
  toasts.value = [...toasts.value, { id, message, tone }]
  window.setTimeout(() => dismissToast(id), 3600)
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((item) => item.id !== id)
}
