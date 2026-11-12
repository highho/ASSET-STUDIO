<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AssetCard from '../components/AssetCard.vue'
import AssetRow from '../components/AssetRow.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import ConvertDialog from '../components/ConvertDialog.vue'
import ScanFolderDialog from '../components/ScanFolderDialog.vue'
import { formatBytes } from '../lib/format'
import { defaultViewMode } from '../lib/prefs'
import {
  assets,
  backfillProgress,
  bootPhase,
  cancelScan,
  categories,
  convertProgress,
  importFiles,
  importProgress,
  initLibrary,
  removeAssets,
  retryThumbnail,
  scanJob,
  scanReport,
  seedExamples,
  totalBytes,
} from '../stores/library'
import { MODEL_FORMATS, MODEL_WITH_SIDECAR_ACCEPT, type SortKey } from '../types'

const router = useRouter()

const query = ref('')
const formatFilter = ref('all')
const categoryFilter = ref('all')
const sortKey = ref<SortKey>('recent')
const viewMode = ref<'grid' | 'list'>(defaultViewMode())
const selectedIds = ref<string[]>([])
const confirmOpen = ref(false)
const scanOpen = ref(false)
const convertOpen = ref(false)
const reportOpen = ref(false)
const dragDepth = ref(0)
const fileInput = ref<HTMLInputElement | null>(null)

// 筛选条直接由 MODEL_FORMATS 生成，加格式只改 types.ts 一处
const formats = ['all', ...MODEL_FORMATS] as const

const sortOptions: Array<{ value: SortKey; label: string }> = [
  { value: 'recent', label: '最近导入' },
  { value: 'name', label: '名称' },
  { value: 'size', label: '文件体积' },
  { value: 'triangles', label: '面数' },
]

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  const list = assets.value.filter((item) => {
    if (formatFilter.value !== 'all' && item.format !== formatFilter.value) return false
    if (categoryFilter.value !== 'all' && (item.category || '未分类') !== categoryFilter.value) return false
    if (!keyword) return true
    return (
      item.name.toLowerCase().includes(keyword) ||
      item.fileName.toLowerCase().includes(keyword) ||
      item.tags.some((tag) => tag.toLowerCase().includes(keyword))
    )
  })

  switch (sortKey.value) {
    case 'name':
      return [...list].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
    case 'size':
      return [...list].sort((a, b) => b.fileSize - a.fileSize)
    case 'triangles':
      return [...list].sort((a, b) => b.stats.triangles - a.stats.triangles)
    default:
      return [...list].sort((a, b) => b.createdAt - a.createdAt)
  }
})

const selectedCount = computed(() => selectedIds.value.length)
const hasFilters = computed(
  () => query.value.trim() !== '' || formatFilter.value !== 'all' || categoryFilter.value !== 'all',
)
const loading = computed(() => bootPhase.value !== 'idle')
const dragging = computed(() => dragDepth.value > 0)
const progressPercent = computed(() => {
  if (!importProgress.value) return 0
  return Math.round((importProgress.value.done / Math.max(importProgress.value.total, 1)) * 100)
})

const backfillPercent = computed(() => {
  if (!backfillProgress.value) return 0
  return Math.round((backfillProgress.value.done / Math.max(backfillProgress.value.total, 1)) * 100)
})

const convertPercent = computed(() => {
  if (!convertProgress.value) return 0
  return Math.round(((convertProgress.value.done + 1) / Math.max(convertProgress.value.total, 1)) * 100)
})

const scanPercent = computed(() => {
  const job = scanJob.value
  if (!job || job.total <= 0) return 0
  return Math.round((job.done / job.total) * 100)
})

function openPicker(): void {
  fileInput.value?.click()
}

function onPick(event: Event): void {
  const input = event.target as HTMLInputElement
  if (input.files) void importFiles([...input.files])
  input.value = ''
}

function toggleSelect(id: string): void {
  selectedIds.value = selectedIds.value.includes(id)
    ? selectedIds.value.filter((item) => item !== id)
    : [...selectedIds.value, id]
}

function openAsset(id: string): void {
  void router.push({ name: 'asset', params: { id } })
}

function resetFilters(): void {
  query.value = ''
  formatFilter.value = 'all'
  categoryFilter.value = 'all'
}

async function confirmDelete(): Promise<void> {
  const ids = [...selectedIds.value]
  confirmOpen.value = false
  selectedIds.value = []
  await removeAssets(ids)
}

function hasFilePayload(event: DragEvent): boolean {
  return Array.from(event.dataTransfer?.types ?? []).includes('Files')
}

function onDragEnter(event: DragEvent): void {
  if (!hasFilePayload(event)) return
  dragDepth.value += 1
}

function onDragOver(event: DragEvent): void {
  if (!hasFilePayload(event)) return
  event.preventDefault()
}

function onDragLeave(event: DragEvent): void {
  if (!hasFilePayload(event)) return
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

function onDrop(event: DragEvent): void {
  event.preventDefault()
  dragDepth.value = 0
  const files = Array.from(event.dataTransfer?.files ?? [])
  if (files.length) void importFiles(files)
}

onMounted(() => {
  void initLibrary()
  window.addEventListener('dragenter', onDragEnter)
  window.addEventListener('dragover', onDragOver)
  window.addEventListener('dragleave', onDragLeave)
  window.addEventListener('drop', onDrop)
})

onBeforeUnmount(() => {
  window.removeEventListener('dragenter', onDragEnter)
  window.removeEventListener('dragover', onDragOver)
  window.removeEventListener('dragleave', onDragLeave)
  window.removeEventListener('drop', onDrop)
})
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <p class="eyebrow mono">LIBRARY / 资产库</p>
        <h1>模型资产</h1>
        <p class="sub">
          <span class="mono">{{ filtered.length }}</span> / {{ assets.length }} 个资产 · 主文件合计
          <span class="mono">{{ formatBytes(totalBytes) }}</span>
        </p>
      </div>
      <div class="head-actions">
        <button class="btn" type="button" @click="router.push('/assembly')">装配台</button>
        <button class="btn" type="button" @click="router.push('/classify')">分类工作台</button>
        <button class="btn" type="button" :disabled="!!importProgress" @click="scanOpen = true">扫描文件夹</button>
        <button class="btn" type="button" @click="router.push('/studio')">用代码新建</button>
        <button class="btn btn-primary" type="button" :disabled="!!importProgress" @click="openPicker">
          {{ importProgress ? '导入中…' : '导入模型' }}
        </button>
      </div>
      <input
        ref="fileInput"
        class="sr-only"
        type="file"
        multiple
        :accept="MODEL_WITH_SIDECAR_ACCEPT"
        @change="onPick"
      />
    </header>

    <div class="toolbar">
      <input v-model="query" class="field search" type="search" placeholder="搜索名称、标签或文件名" aria-label="搜索资产" />

      <div class="format-chips">
        <button
          v-for="format in formats"
          :key="format"
          class="chip"
          type="button"
          :class="{ 'is-active': formatFilter === format }"
          @click="formatFilter = format"
        >
          {{ format === 'all' ? '全部格式' : format }}
        </button>
      </div>

      <select v-model="categoryFilter" class="field compact" aria-label="按分类筛选">
        <option value="all">全部分类</option>
        <option v-for="category in categories" :key="category" :value="category">{{ category }}</option>
      </select>

      <select v-model="sortKey" class="field compact" aria-label="排序方式">
        <option v-for="option in sortOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
      </select>

      <div class="view-switch">
        <button
          class="chip"
          type="button"
          :class="{ 'is-active': viewMode === 'grid' }"
          :aria-pressed="viewMode === 'grid'"
          @click="viewMode = 'grid'"
        >
          网格
        </button>
        <button
          class="chip"
          type="button"
          :class="{ 'is-active': viewMode === 'list' }"
          :aria-pressed="viewMode === 'list'"
          @click="viewMode = 'list'"
        >
          列表
        </button>
      </div>
    </div>

    <div v-if="scanJob" class="import-bar">
      <div class="import-head mono">
        <span>
          扫描 {{ scanJob.total > 0 ? `${scanJob.done} / ${scanJob.total}` : '遍历目录中…' }}
          <template v-if="scanJob.skipped > 0"> · 跳过 {{ scanJob.skipped }}</template>
          <template v-if="scanJob.failed > 0"> · 失败 {{ scanJob.failed }}</template>
        </span>
        <span class="import-file">{{ scanJob.message || '正在收集候选文件' }}</span>
        <button class="btn btn-sm btn-ghost" type="button" @click="cancelScan">取消</button>
      </div>
      <div class="import-track"><span :style="{ width: `${scanPercent}%` }"></span></div>
    </div>

    <div v-if="importProgress" class="import-bar">
      <div class="import-head mono">
        <span>导入 {{ importProgress.done + 1 }} / {{ importProgress.total }}</span>
        <span class="import-file">{{ importProgress.label }}</span>
      </div>
      <div class="import-track"><span :style="{ width: `${progressPercent}%` }"></span></div>
    </div>

    <div v-if="backfillProgress" class="import-bar is-quiet">
      <div class="import-head mono">
        <span>补齐几何统计与缩略图 {{ backfillProgress.done }} / {{ backfillProgress.total }}</span>
        <span class="import-file">扫描入库的模型正在逐个解析</span>
      </div>
      <div class="import-track"><span :style="{ width: `${backfillPercent}%` }"></span></div>
    </div>

    <div v-if="convertProgress" class="import-bar">
      <div class="import-head mono">
        <span>格式转换 {{ convertProgress.done + 1 }} / {{ convertProgress.total }}</span>
        <span class="import-file">{{ convertProgress.label }}</span>
      </div>
      <div class="import-track"><span :style="{ width: `${convertPercent}%` }"></span></div>
    </div>

    <div v-if="scanReport" class="scan-report">
      <div class="scan-head">
        <span class="mono scan-code">SCAN</span>
        <span class="scan-summary">
          新增 {{ scanReport.created }} · 跳过 {{ scanReport.skipped }} · 失败 {{ scanReport.failed.length }}
        </span>
        <span v-if="scanReport.truncated" class="scan-warn mono">已达单次上限</span>
        <button class="btn btn-sm btn-ghost" type="button" @click="scanReport = null">关闭</button>
      </div>
      <div v-if="scanReport.failed.length" class="scan-fail">
        <button class="scan-link" type="button" @click="reportOpen = !reportOpen">
          {{ reportOpen ? '收起失败明细' : '查看失败明细' }}
        </button>
        <ul v-if="reportOpen">
          <li v-for="item in scanReport.failed" :key="item.path">
            <span class="mono">{{ item.path }}</span>
            <em>{{ item.reason }}</em>
          </li>
        </ul>
      </div>
    </div>

    <div v-if="selectedCount > 0" class="bulk">
      <span class="mono">已选 {{ selectedCount }} 项</span>
      <button class="btn btn-sm" type="button" @click="convertOpen = true">批量转换</button>
      <button class="btn btn-sm btn-danger" type="button" @click="confirmOpen = true">删除所选</button>
      <button class="btn btn-sm btn-ghost" type="button" @click="selectedIds = []">清除选择</button>
    </div>

    <div v-if="loading" class="state">
      <p class="mono state-code">PREPARING</p>
      <h2>{{ bootPhase === 'seeding' ? '正在准备内置示例资产' : '正在读取资产库' }}</h2>
      <p>首次启动会把示例模型写入本机 SQLite 库与 data/models 目录，只需几秒。</p>
    </div>

    <div v-else-if="assets.length === 0" class="state">
      <p class="mono state-code">EMPTY LIBRARY</p>
      <h2>资产库还是空的</h2>
      <p>把 GLB、GLTF、OBJ、STL 或 FBX 文件拖到窗口任意位置，或点击导入按钮。</p>
      <div class="state-actions">
        <button class="btn btn-primary" type="button" @click="openPicker">导入第一个模型</button>
        <button class="btn" type="button" :disabled="bootPhase !== 'idle'" @click="seedExamples">
          载入 4 个示例资产
        </button>
      </div>
    </div>

    <div v-else-if="filtered.length === 0" class="state">
      <p class="mono state-code">NO MATCH</p>
      <h2>没有符合当前条件的资产</h2>
      <p>换一个关键词，或清空格式与分类筛选。</p>
      <button class="btn" type="button" @click="resetFilters">清空筛选</button>
    </div>

    <div v-else-if="viewMode === 'grid'" class="grid">
      <AssetCard
        v-for="asset in filtered"
        :key="asset.id"
        :asset="asset"
        :selected="selectedIds.includes(asset.id)"
        @open="openAsset(asset.id)"
        @toggle="toggleSelect(asset.id)"
        @retry-thumb="retryThumbnail(asset.id)"
      />
    </div>

    <div v-else class="list">
      <div class="list-head mono">
        <span class="head-select"></span>
        <span class="head-name">名称 / 分类</span>
        <span>格式</span>
        <span>面数</span>
        <span>体积</span>
        <span>导入时间</span>
      </div>
      <AssetRow
        v-for="asset in filtered"
        :key="asset.id"
        :asset="asset"
        :selected="selectedIds.includes(asset.id)"
        @open="openAsset(asset.id)"
        @toggle="toggleSelect(asset.id)"
      />
    </div>

    <div v-if="dragging" class="drop-overlay">
      <div class="drop-box">
        <p class="mono">DROP TO IMPORT</p>
        <h2>松开即导入模型</h2>
        <p class="mono">GLB · GLTF · OBJ · STL · FBX ＋ .bin / .mtl / 贴图</p>
      </div>
    </div>

    <ConfirmDialog
      :open="confirmOpen"
      title="删除所选资产"
      :message="`将删除 ${selectedCount} 个资产的模型文件与缩略图，磁盘上的文件不可恢复。`"
      confirm-label="删除"
      @cancel="confirmOpen = false"
      @confirm="confirmDelete"
    />

    <ScanFolderDialog :open="scanOpen" @close="scanOpen = false" />

    <ConvertDialog :open="convertOpen" :ids="selectedIds" @close="convertOpen = false" />
  </section>
</template>

<style scoped>
.page {
  position: relative;
  height: 100%;
  overflow-y: auto;
  padding: 8px 10px 24px;
  background: var(--surface);
}

.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 8px;
  padding-bottom: 6px;
  border-bottom: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
}

.eyebrow {
  color: #333333;
  font-size: 9px;
  letter-spacing: 0;
}

h1 {
  margin-top: 1px;
  font-size: 18px;
  line-height: 1.15;
}

.sub {
  margin-top: 2px;
  color: #333333;
  font-size: 11px;
}

.sub .mono {
  color: #000080;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 3px;
  flex: none;
}

.import-bar.is-quiet {
  border-color: var(--line-strong);
  background: var(--surface-2);
}

.import-bar.is-quiet .import-track span {
  background: var(--text-faint);
}

.scan-report {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 14px;
  padding: 10px 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  background: var(--surface-2);
}

.scan-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.scan-code {
  padding: 2px 5px;
  border: 1px solid var(--accent-dim);
  border-radius: 2px;
  color: var(--accent);
  font-size: 9.5px;
  letter-spacing: 0.1em;
}

.scan-summary {
  flex: 1;
  color: var(--text-dim);
  font-size: 12px;
}

.scan-warn {
  color: var(--danger);
  font-size: 10px;
}

.scan-fail {
  padding-left: 2px;
}

.scan-link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--text-dim);
  font-size: 11.5px;
  text-decoration: underline;
  cursor: pointer;
}

.scan-link:hover {
  color: var(--accent);
}

.scan-fail ul {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}

.scan-fail li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 11px;
}

.scan-fail li .mono {
  color: var(--text-dim);
  word-break: break-all;
}

.scan-fail li em {
  color: var(--danger);
  font-style: normal;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  padding: 6px 0 5px;
}

.search {
  flex: 1 1 220px;
  max-width: 320px;
}

.format-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.compact {
  width: auto;
  min-width: 108px;
}

.view-switch {
  display: flex;
  gap: 4px;
  margin-left: auto;
}

.import-bar {
  margin-bottom: 12px;
  padding: 10px 12px;
  border: 1px solid var(--accent-dim);
  border-radius: var(--radius);
  background: var(--accent-soft);
}

.import-head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: var(--text-dim);
  font-size: 11px;
}

.import-file {
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.import-head .btn {
  flex: none;
  align-self: center;
}

.import-track {
  height: 3px;
  margin-top: 8px;
  border-radius: 2px;
  background: var(--bg);
  overflow: hidden;
}

.import-track span {
  display: block;
  height: 100%;
  background: var(--accent);
  transition: width 240ms ease;
}

.bulk {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  padding: 8px 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--surface-2);
}

.bulk .mono {
  color: var(--accent);
  font-size: 11.5px;
}

.state {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  max-width: 460px;
  margin: 24px auto 0;
  padding: 8px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
  text-align: left;
}

.state-code {
  color: #000080;
  font-size: 10px;
  letter-spacing: 0;
}

.state-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 3px;
}

.state h2 {
  font-size: 12px;
}

.state p {
  color: #333333;
  font-size: 10.5px;
  line-height: 1.5;
}

.state .btn {
  margin-top: 2px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(218px, 1fr));
  gap: 7px;
}

.list {
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  overflow: hidden;
  background: #ffffff;
}

.list-head {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr) 52px 72px 80px 72px;
  gap: 8px;
  padding: 4px 6px;
  background: #c0c0c0;
  color: #000000;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0;
}

.list-head span:nth-child(n + 3) {
  text-align: right;
}

.head-select {
  display: block;
}

.head-name {
  text-align: left !important;
}

.drop-overlay {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: grid;
  place-items: center;
  background: rgba(0, 0, 0, 0.55);
}

.drop-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 16px 24px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: #d4d0c8;
  text-align: center;
}

.drop-box p:first-child {
  color: #000080;
  font-size: 10px;
  letter-spacing: 0;
}

.drop-box h2 {
  font-size: 13px;
}

.drop-box p:last-child {
  color: #333333;
  font-size: 10px;
  letter-spacing: 0;
}

@media (max-width: 900px) {
  .page {
    padding: 8px 10px 24px;
  }

  h1 {
    font-size: 18px;
  }

  .view-switch {
    margin-left: 0;
  }

  .list-head {
    display: none;
  }
}
</style>
