<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { focusFirst, trapTab } from '../lib/focus'
import { api, type MmdStatus } from '../lib/api'
import { scanFolder } from '../stores/library'
import { SCAN_FORMATS, type ScanFormat } from '../types'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const form = reactive({ path: '', category: '', recursive: true, limit: 2000 })
const formats = ref<ScanFormat[]>([...SCAN_FORMATS])
const mmd = ref<MmdStatus | null>(null)
const submitting = ref(false)
const touched = ref(false)
const dialogEl = ref<HTMLElement | null>(null)

// PMX 与 .blend 的依赖不同，分开提示：没有 mmd_tools 时 .blend 仍能转。
const mmdNotice = computed(() => {
  if (!mmd.value) return ''
  if (formats.value.includes('pmx') && !mmd.value.pmxReady) {
    return 'PMX 需要 mmd_tools 与 opencc：' + (mmd.value.error || '运行时未就绪')
  }
  if (formats.value.includes('blend') && !mmd.value.available) {
    return '.blend 需要 Blender：' + (mmd.value.error || '运行时未就绪')
  }
  return ''
})

async function refreshMmdStatus(): Promise<void> {
  try {
    mmd.value = await api.mmdStatus()
  } catch {
    mmd.value = { available: false, pmxReady: false, error: '无法读取转换运行时状态' }
  }
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      submitting.value = false
      touched.value = false
      formats.value = [...SCAN_FORMATS]
      void refreshMmdStatus()
      window.addEventListener('keydown', onKeydown)
      void nextTick(() => focusFirst(dialogEl.value))
      return
    }
    window.removeEventListener('keydown', onKeydown)
  },
)

onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    if (!submitting.value) emit('close')
    return
  }
  if (event.key === 'Tab') trapTab(event, dialogEl.value)
}

function toggleFormat(format: ScanFormat): void {
  formats.value = formats.value.includes(format)
    ? formats.value.filter((item) => item !== format)
    : [...formats.value, format]
}

async function submit(): Promise<void> {
  touched.value = true
  const path = form.path.trim()
  if (!path || submitting.value) return
  submitting.value = true
  try {
    // 扫描是后台作业：这里只负责发起，进度与取消都在资产库页面上。
    await scanFolder({
      path,
      recursive: form.recursive,
      category: form.category.trim() || '未分类',
      formats: formats.value,
      limit: form.limit,
    })
    emit('close')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="backdrop" @click.self="emit('close')" @keydown="onKeydown">
      <div ref="dialogEl" class="dialog" role="dialog" aria-modal="true" aria-label="扫描文件夹批量导入" tabindex="-1">
        <p class="mono eyebrow">FOLDER SCAN / 批量导入</p>
        <h2 class="dialog-title">扫描本地文件夹</h2>
        <p class="dialog-message">
          填写一个本机文件夹路径，服务端会把里面的模型复制进资产库并登记来源路径。
          扫描在后台跑，进度条和「取消」在资产库页面上；等待期间可以照常浏览、删除资产。
        </p>

        <label class="field-group">
          <span class="label">文件夹绝对路径</span>
          <input
            v-model="form.path"
            class="field mono"
            type="text"
            placeholder="D:\models 或 C:\Users\...\assets"
            @keyup.enter="submit"
          />
        </label>
        <p v-if="touched && !form.path.trim()" class="hint-error">请先填写文件夹路径</p>

        <label class="field-group">
          <span class="label">入库分类</span>
          <input v-model="form.category" class="field" type="text" placeholder="未分类" maxlength="32" />
        </label>

        <div class="field-group">
          <span class="label">扫描格式</span>
          <div class="chips">
            <button
              v-for="item in SCAN_FORMATS"
              :key="item"
              class="chip mono"
              :class="{ 'is-on': formats.includes(item) }"
              type="button"
              @click="toggleFormat(item)"
            >
              {{ item.toUpperCase() }}
            </button>
          </div>
          <p v-if="touched && formats.length === 0" class="hint-error">至少选择一种格式</p>
          <p class="foot-note mono">
            PMX / .BLEND 由服务端调用 Blender 转成 GLB 后入库；PMX 需要 mmd_tools + opencc。
          </p>
          <p v-if="mmdNotice" class="hint-error">{{ mmdNotice }}</p>
        </div>

        <label class="check">
          <input v-model="form.recursive" type="checkbox" />
          <span>包含子文件夹</span>
        </label>
        <p class="foot-note mono">单次最多 2000 个文件 · 隐藏文件与 node_modules 等目录自动跳过</p>

        <div class="dialog-actions">
          <button class="btn" type="button" @click="emit('close')">取消</button>
          <button
            class="btn btn-primary"
            type="button"
            :disabled="submitting || formats.length === 0"
            @click="submit"
          >
            {{ submitting ? '扫描中…' : '开始扫描' }}
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
  width: min(480px, 100%);
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

.field-group {
  display: block;
}

.field-group .label {
  margin-bottom: 2px;
}

.hint-error {
  margin-top: -2px;
  color: #800000;
  font-size: 10px;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 3px;
  margin-top: 2px;
}

.chip {
  padding: 1px 5px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: #c0c0c0;
  color: #000000;
  font-size: 10px;
  letter-spacing: 0;
  cursor: pointer;
}

.chip.is-on {
  border-color: var(--bevel-sunken);
  background: #ffffff;
  color: #000080;
}

.foot-note {
  color: #333333;
  font-size: 9.5px;
  line-height: 1.4;
}

.check {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 2px;
  color: #000000;
  font-size: 10px;
}

.check input {
  accent-color: #000080;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 3px;
  margin-top: 4px;
}
</style>
