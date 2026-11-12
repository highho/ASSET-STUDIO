<script setup lang="ts">
import { dismissToast, toasts } from '../stores/toast'
</script>

<template>
  <div class="toast-stack">
    <button
      v-for="toast in toasts"
      :key="toast.id"
      class="toast"
      :class="`is-${toast.tone}`"
      type="button"
      @click="dismissToast(toast.id)"
    >
      <span class="toast-tone mono">{{ toast.tone === 'error' ? 'ERR' : toast.tone === 'success' ? 'OK' : 'INFO' }}</span>
      <span class="toast-text">{{ toast.message }}</span>
    </button>
  </div>
</template>

<style scoped>
.toast-stack {
  position: fixed;
  right: 18px;
  bottom: 18px;
  z-index: 60;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: 380px;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  background: var(--surface-2);
  box-shadow: var(--shadow);
  color: var(--text);
  text-align: left;
  font-size: 12.5px;
  animation: toast-in 220ms ease both;
}

.toast.is-success {
  border-left: 2px solid var(--ok);
}

.toast.is-error {
  border-left: 2px solid var(--danger);
}

.toast.is-info {
  border-left: 2px solid var(--accent);
}

.toast-tone {
  padding-top: 1px;
  color: var(--text-faint);
  font-size: 10px;
  letter-spacing: 0.08em;
}

.toast-text {
  line-height: 1.5;
}

@keyframes toast-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
