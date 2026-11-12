<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { convertLossNote } from '../lib/convert-loss'
import { focusFirst, trapTab } from '../lib/focus'
import { convertAssets } from '../stores/library'
import { CONVERT_TARGETS, type ConvertTarget } from '../types'

const props = defineProps<{ open: boolean; ids: string[] }>()
const emit = defineEmits<{ close: [] }>()

const target = ref<ConvertTarget>('glb')
const submitting = ref(false)
const dialogEl = ref<HTMLElement | null>(null)

watch(
  () => props.open,
  (open) => {
    if (!open) {
      window.removeEventListener('keydown', onKeydown)
      return
    }
    submitting.value = false
    window.addEventListener('keydown', onKeydown)
    void nextTick(() => focusFirst(dialogEl.value))
  },
)

onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

const count = computed(() => props.ids.length)

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    // 转换已经在跑时不关：关掉不等于取消，容易让人以为没转换
    if (!submitting.value) emit('close')
    return
  }
  if (event.key === 'Tab') trapTab(event, dialogEl.value)
}

async function submit(): Promise<void> {
  if (!count.value || submitting.value) return
  submitting.value = true
  try {
    await convertAssets(props.ids, target.value)
    emit('close')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="backdrop" @click.self="emit('close')" @keydown="onKeydown">
      <div ref="dialogEl" class="dialog" role="dialog" aria-modal="true" aria-label="模型格式转换" tabindex="-1">
        <p class="mono eyebrow">FORMAT CONVERT / 格式转换</p>
        <h2 class="dialog-title">把 {{ count }} 个资产转换成…</h2>
        <p class="dialog-message">
          转换在浏览器里完成，结果作为新资产入库，原件保留不动。
          新条目会带上「格式转换」标签并在描述里记录来源。
        </p>

        <div class="targets">
          <button
            v-for="item in CONVERT_TARGETS"
            :key="item"
            class="target"
            :class="{ 'is-active': target === item }"
            type="button"
            @click="target = item"
          >
            <span class="target-code mono">{{ item.toUpperCase() }}</span>
            <span class="target-note">{{ convertLossNote(item) }}</span>
          </button>
        </div>

        <p class="foot-note mono">FBX 只支持导入，Three.js 没有 FBX 导出器</p>

        <div class="dialog-actions">
          <button class="btn" type="button" @click="emit('close')">取消</button>
          <button class="btn btn-primary" type="button" :disabled="submitting || count === 0" @click="submit">
            {{ submitting ? '转换中…' : `转换为 ${target.toUpperCase()}` }}
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
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: min(520px, 100%);
  padding: 6px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: var(--surface);
  box-shadow: 2px 2px 0 #000000;
}

.eyebrow {
  color: #333333;
  font-size: 9px;
  letter-spacing: 0;
}

.dialog-title {
  padding-bottom: 3px;
  border-bottom: 1px solid #808080;
  font-size: 11px;
  font-weight: 700;
  color: #000080;
}

.dialog-message {
  color: #000000;
  font-size: 10px;
  line-height: 1.5;
}

.targets {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 3px;
  margin-top: 2px;
}

.target {
  display: flex;
  flex-direction: column;
  gap: 3px;
  align-items: flex-start;
  padding: 4px 5px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: #d4d0c8;
  color: #000000;
  text-align: left;
  cursor: pointer;
}

.target:hover {
  border-color: #000080;
}

.target.is-active {
  border-color: var(--bevel-sunken);
  background: #ffffff;
}

.target-code {
  font-size: 10px;
  letter-spacing: 0;
  color: #000080;
}

.target-note {
  color: #333333;
  font-size: 10px;
  line-height: 1.4;
}

.foot-note {
  color: #333333;
  font-size: 9.5px;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 3px;
  margin-top: 4px;
}
</style>
