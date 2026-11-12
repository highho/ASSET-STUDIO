<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { formatBytes } from '../lib/format'
import { readError } from '../lib/download'
import { assets, classifyAssetMeta, initLibrary, saveAssetMeta } from '../stores/library'
import { pushToast } from '../stores/toast'
import type { AssetMeta } from '../types'

onMounted(() => {
  void initLibrary()
})

const classifyQuery = ref('')
const categoryFilter = ref('all')
const onlyUnclassified = ref(false)
const selectedIds = ref<string[]>([])
const analyzingIds = ref<string[]>([])
const batchTotal = ref(0)
const batchDone = ref(0)
const batchRunning = ref(false)

const categories = computed(() => {
  const seen = new Set<string>()
  for (const asset of assets.value) {
    const value = asset.category || '未分类'
    if (value) seen.add(value)
  }
  return [...seen].sort((a, b) => a.localeCompare(b, 'zh-CN'))
})

const filteredAssets = computed(() => {
  const keyword = classifyQuery.value.trim().toLowerCase()
  return assets.value.filter((asset) => {
    if (categoryFilter.value !== 'all' && (asset.category || '未分类') !== categoryFilter.value) return false
    if (onlyUnclassified.value && asset.ai?.category) return false
    if (!keyword) return true
    return (
      asset.name.toLowerCase().includes(keyword) ||
      asset.format.toLowerCase().includes(keyword) ||
      asset.tags.some((tag) => tag.toLowerCase().includes(keyword))
    )
  })
})

const visibleIds = computed(() => filteredAssets.value.map((asset) => asset.id))
const allVisibleSelected = computed(
  () => visibleIds.value.length > 0 && visibleIds.value.every((id) => selectedIds.value.includes(id)),
)
const selectedCount = computed(() => selectedIds.value.length)
const selectedWithSuggestion = computed(
  () => assets.value.filter((asset) => selectedIds.value.includes(asset.id) && asset.ai?.category).length,
)
const analyzedCount = computed(() => assets.value.filter((asset) => asset.ai?.category).length)
const pendingCount = computed(() => assets.value.length - analyzedCount.value)

function toggleVisible(): void {
  if (allVisibleSelected.value) {
    const visible = new Set(visibleIds.value)
    selectedIds.value = selectedIds.value.filter((id) => !visible.has(id))
    return
  }
  const merged = new Set(selectedIds.value)
  for (const id of visibleIds.value) merged.add(id)
  selectedIds.value = [...merged]
}

function isAnalyzing(id: string): boolean {
  return analyzingIds.value.includes(id)
}

async function analyzeAsset(asset: AssetMeta): Promise<boolean> {
  if (isAnalyzing(asset.id)) return false
  analyzingIds.value.push(asset.id)
  try {
    await classifyAssetMeta(asset.id)
    return true
  } catch (error) {
    pushToast(`「${asset.name}」分析失败：${readError(error)}`, 'error')
    return false
  } finally {
    analyzingIds.value = analyzingIds.value.filter((id) => id !== asset.id)
  }
}

async function analyzeSelected(): Promise<void> {
  if (batchRunning.value || !selectedIds.value.length) return
  const targets = assets.value.filter((asset) => selectedIds.value.includes(asset.id))
  batchRunning.value = true
  batchTotal.value = targets.length
  batchDone.value = 0
  let ok = 0
  for (const asset of targets) {
    if (await analyzeAsset(asset)) ok += 1
    batchDone.value += 1
  }
  batchRunning.value = false
  pushToast(`批量分析完成：成功 ${ok} / ${targets.length}`, ok === targets.length ? 'success' : 'error')
}

async function adoptSuggestion(asset: AssetMeta): Promise<void> {
  const suggestion = asset.ai
  if (!suggestion?.category) return
  const tags = [...new Set([...asset.tags, ...(suggestion.tags ?? [])])]
  const updated = await saveAssetMeta({
    id: asset.id,
    name: asset.name,
    description: asset.description,
    category: suggestion.category,
    tags,
  })
  if (updated) pushToast(`已采纳「${asset.name}」的 AI 建议`, 'success')
}

async function adoptSelected(): Promise<void> {
  const targets = assets.value.filter((asset) => selectedIds.value.includes(asset.id) && asset.ai?.category)
  if (!targets.length) return
  let ok = 0
  for (const asset of targets) {
    const tags = [...new Set([...asset.tags, ...(asset.ai?.tags ?? [])])]
    const updated = await saveAssetMeta({
      id: asset.id,
      name: asset.name,
      description: asset.description,
      category: asset.ai?.category ?? '',
      tags,
    })
    if (updated) ok += 1
  }
  pushToast(`已采纳 ${ok} / ${targets.length} 条建议`, ok === targets.length ? 'success' : 'error')
}

function categoryLabel(asset: AssetMeta): string {
  return asset.category || '未分类'
}
</script>

<template>
  <section class="classify-page">
    <header class="page-head">
      <div>
        <p class="eyebrow mono">CLASSIFY / 分类工作台</p>
        <h1>资产分类工作台</h1>
        <p class="sub">批量分析当前资产库 · AI 只给建议，采纳后才改正式分类</p>
      </div>
      <div class="head-side">
        <span class="mono">{{ analyzedCount }}/{{ assets.length }} 已分析 · {{ pendingCount }} 待分析</span>
        <RouterLink to="/settings?tab=model" class="btn btn-sm">模型配置</RouterLink>
      </div>
    </header>

    <section class="panel classify-panel">
      <div class="panel-head">
        <h2>待分类资产</h2>
        <span class="mono panel-note">搜索、筛选后可勾选批量分析</span>
      </div>
      <div class="classify-tools">
        <input v-model="classifyQuery" class="field search" type="search" placeholder="搜索名称、标签、格式" aria-label="搜索分类资产" />
        <select v-model="categoryFilter" class="field compact" aria-label="筛选分类">
          <option value="all">全部分类</option>
          <option v-for="category in categories" :key="category" :value="category">{{ category }}</option>
        </select>
        <label class="mini-check">
          <input v-model="onlyUnclassified" type="checkbox" />
          仅未分析
        </label>
      </div>
      <div class="classify-actions">
        <button class="btn btn-sm" type="button" :disabled="!selectedIds.length || batchRunning" @click="analyzeSelected">
          {{ batchRunning ? `分析中 ${batchDone}/${batchTotal}` : `批量分析（${selectedCount}）` }}
        </button>
        <button class="btn btn-sm" type="button" :disabled="!selectedWithSuggestion || batchRunning" @click="adoptSelected">
          采纳选中（{{ selectedWithSuggestion }}）
        </button>
        <span class="mono selected-note">AI 只给建议，采纳后才改正式分类</span>
      </div>
      <div class="table-wrap">
        <table class="ai-table">
          <thead>
            <tr>
              <th class="check-col">
                <input type="checkbox" :checked="allVisibleSelected" aria-label="全选当前列表" @change="toggleVisible" />
              </th>
              <th>资产</th>
              <th>正式分类</th>
              <th>AI 建议</th>
              <th class="action-col">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="asset in filteredAssets" :key="asset.id" :class="{ 'is-selected': selectedIds.includes(asset.id) }">
              <td class="check-col">
                <input v-model="selectedIds" type="checkbox" :value="asset.id" :aria-label="`选择 ${asset.name}`" />
              </td>
              <td class="asset-cell">
                <RouterLink :to="`/asset/${asset.id}`" class="asset-link">{{ asset.name }}</RouterLink>
                <span class="asset-meta mono">{{ asset.format }} · {{ formatBytes(asset.fileSize) }}</span>
              </td>
              <td><span class="tag-pill">{{ categoryLabel(asset) }}</span></td>
              <td>
                <span v-if="asset.ai?.category" class="ai-pill">
                  {{ asset.ai.category }} · {{ Math.round((asset.ai.confidence ?? 0) * 100) }}%
                </span>
                <span v-else class="muted mono">—</span>
              </td>
              <td class="action-col">
                <button class="btn btn-sm" type="button" :disabled="isAnalyzing(asset.id) || batchRunning" @click="analyzeAsset(asset)">
                  {{ isAnalyzing(asset.id) ? '分析中' : '分析' }}
                </button>
                <button class="btn btn-sm" type="button" :disabled="!asset.ai?.category || batchRunning" @click="adoptSuggestion(asset)">采纳</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!filteredAssets.length" class="table-empty mono">没有符合筛选条件的资产</div>
      </div>
    </section>
  </section>
</template>

<style scoped>
.classify-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  padding: 8px 10px 10px;
  background: var(--surface);
}

.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 8px;
  flex: none;
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

.head-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 3px;
  flex: none;
  color: #333333;
  font-size: 9px;
}

.panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  margin-top: 8px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  background: var(--surface);
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 2px 5px;
  border-bottom: 1px solid #808080;
  background: #c0c0c0;
}

.panel-head h2 {
  font-family: var(--font-display);
  font-size: 11px;
  font-weight: 700;
}

.panel-note {
  color: #333333;
  font-size: 9px;
  text-align: right;
}

.classify-tools {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 5px 6px 0;
}

.classify-tools .search {
  min-width: 0;
  flex: 1;
}

.classify-tools .compact {
  width: 110px;
  flex: none;
}

.mini-check {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: none;
  color: #333333;
  font-size: 9px;
  white-space: nowrap;
}

.classify-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 6px;
}

.selected-note {
  margin-left: auto;
  color: #666666;
  font-size: 8px;
  text-align: right;
}

.table-wrap {
  flex: 1;
  min-height: 200px;
  margin: 0 6px 6px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
  overflow: auto;
}

.ai-table {
  width: 100%;
  min-width: 620px;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 9px;
}

.ai-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  padding: 3px;
  border-right: 1px solid #808080;
  border-bottom: 1px solid #808080;
  background: #c0c0c0;
  color: #000000;
  font-weight: 700;
  text-align: left;
  white-space: nowrap;
}

.ai-table td {
  padding: 2px 3px;
  border-right: 1px solid #d4d0c8;
  border-bottom: 1px solid #e0ddd6;
  vertical-align: middle;
}

.ai-table tr.is-selected td {
  background: #e8eefc;
}

.ai-table .check-col {
  width: 20px;
  text-align: center;
}

.ai-table .action-col {
  width: 88px;
  white-space: nowrap;
}

.ai-table th:nth-child(3) {
  width: 80px;
}

.ai-table th:nth-child(4) {
  width: 120px;
}

.ai-table .asset-cell {
  overflow: hidden;
  white-space: nowrap;
}

.asset-link {
  display: block;
  overflow: hidden;
  color: #000080;
  text-overflow: ellipsis;
}

.asset-link:hover {
  text-decoration: underline;
}

.asset-meta {
  display: block;
  color: #666666;
  font-size: 8px;
}

.tag-pill,
.ai-pill {
  display: inline-block;
  max-width: 100%;
  padding: 0 3px;
  border: 1px solid #808080;
  background: #d4d0c8;
  color: #000000;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 8px;
  line-height: 13px;
}

.ai-pill {
  border-color: #000080;
  background: #e8eefc;
  color: #000080;
}

.muted {
  color: #999999;
}

.table-empty {
  padding: 10px;
  color: #666666;
  font-size: 10px;
  text-align: center;
}

@media (max-width: 620px) {
  .head-side {
    display: none;
  }

  .classify-tools {
    flex-wrap: wrap;
  }

  .classify-tools .search {
    flex-basis: 100%;
  }

  .selected-note {
    display: none;
  }
}
</style>
