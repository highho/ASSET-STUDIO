<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  title: string
  message: string
  confirmLabel?: string
}>()

const emit = defineEmits<{
  confirm: []
  cancel: []
}>()

const dialog = ref<HTMLElement | null>(null)
const cancelButton = ref<HTMLButtonElement | null>(null)
let lastFocused: HTMLElement | null = null

/** 取消按钮是默认落点：破坏性操作的默认焦点不能落在「确认」上 */
watch(
  () => props.open,
  async (open) => {
    if (open) {
      lastFocused = document.activeElement as HTMLElement | null
      await nextTick()
      cancelButton.value?.focus()
    } else {
      lastFocused?.focus?.()
      lastFocused = null
    }
  },
)

function onKeydown(event: KeyboardEvent): void {
  if (!props.open) return
  if (event.key === 'Escape') {
    event.stopPropagation()
    emit('cancel')
    return
  }
  if (event.key !== 'Tab') return
  // 模态框里只有一个「确定」和一个「取消」，Tab 到边界时手动绕回，焦点不会漏到背后页面
  const focusable = dialog.value?.querySelectorAll<HTMLElement>('button, [href], input, select, textarea')
  if (!focusable || focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="backdrop" @click.self="emit('cancel')" @keydown="onKeydown">
      <div
        ref="dialog"
        class="dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
      >
        <h2 class="dialog-title">{{ title }}</h2>
        <p class="dialog-message">{{ message }}</p>
        <div class="dialog-actions">
          <button ref="cancelButton" class="btn" type="button" @click="emit('cancel')">取消</button>
          <button class="btn btn-danger" type="button" @click="emit('confirm')">
            {{ confirmLabel ?? '确认' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.55);
}

.dialog {
  width: min(360px, 100%);
  padding: 6px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: var(--surface);
  box-shadow: 2px 2px 0 #000000;
}

.dialog-title {
  padding-bottom: 3px;
  border-bottom: 1px solid #808080;
  font-size: 11px;
  font-weight: 700;
  color: #000080;
  margin-bottom: 6px;
}

.dialog-message {
  color: #000000;
  font-size: 11px;
  line-height: 1.5;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 3px;
  margin-top: 8px;
}
</style>
