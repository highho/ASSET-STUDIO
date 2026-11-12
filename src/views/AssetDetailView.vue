<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import * as THREE from 'three'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import ConvertDialog from '../components/ConvertDialog.vue'
import ModelViewer from '../components/ModelViewer.vue'
import SandLab from '../components/SandLab.vue'
import { api } from '../lib/api'
import { ASSEMBLY_SIDECAR, parseAssembly } from '../lib/assembly'
import { downloadBlob, readError } from '../lib/download'
import { formatBytes, formatCount, formatDate, formatDimensions } from '../lib/format'
import { disposeObject, loadModelFromBlob } from '../lib/model'
import {
  classifyAssetMeta,
  getAsset,
  initLibrary,
  readAssetFile,
  regenerateThumbnail,
  removeAssets,
  restoreAssetVersion,
  saveAssetMeta,
} from '../stores/library'
import { pushToast } from '../stores/toast'
import { dimensionUnitLabel, type AssetMeta, type AssetVersion } from '../types'

const props = defineProps<{ id: string }>()
const router = useRouter()

const asset = ref<AssetMeta | null>(null)
const model = shallowRef<THREE.Object3D | null>(null)
const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const classifying = ref(false)
const confirmOpen = ref(false)
const convertOpen = ref(false)
/** 检视台三模式是同一个资产的三种表示：素模 / 带贴图的实景 / 它自己的 sand3d 源码 */
const viewMode = ref<'model' | 'realistic' | 'code'>('realistic')
const MODE_ORDER = ['model', 'realistic', 'code'] as const
/** 带 assembly.json 的资产是装配台合成的：能重新打开继续摆，而不是只能看/下载 */
const assemblyParts = ref(0)

/** 标签组按左右方向键切换：只靠鼠标点的话，键盘用户切不过去 */
function onTabKey(event: KeyboardEvent, mode: (typeof MODE_ORDER)[number]): void {
  const step = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
  if (step === 0) return
  event.preventDefault()
  const index = MODE_ORDER.indexOf(mode)
  const next = MODE_ORDER[(index + step + MODE_ORDER.length) % MODE_ORDER.length]
  viewMode.value = next
  const tab = (event.currentTarget as HTMLElement).parentElement?.querySelector<HTMLElement>(`[data-mode="${next}"]`)
  tab?.focus()
}

const draft = reactive({
  name: '',
  category: '',
  tagsText: '',
  description: '',
})

/** 版本记录：当前版是资产本身，列表里只放被替换下来的旧版（最新在前） */
const versions = ref<AssetVersion[]>([])
const versionsLoading = ref(false)
const restoreTarget = ref<number | null>(null)
const restoring = ref(false)

const dirty = computed(() => {
  const current = asset.value
  if (!current) return false
  return (
    draft.name !== current.name ||
    draft.category !== current.category ||
    draft.tagsText !== current.tags.join(', ') ||
    draft.description !== current.description
  )
})

function resetDraft(meta: AssetMeta): void {
  draft.name = meta.name
  draft.category = meta.category
  draft.tagsText = meta.tags.join(', ')
  draft.description = meta.description
}

function disposeModel(): void {
  const current = model.value
  model.value = null
  if (current) window.setTimeout(() => disposeObject(current), 0)
}

async function loadVersions(id: string): Promise<void> {
  versionsLoading.value = true
  try {
    const list = await api.versions(id)
    versions.value = list.items
  } catch {
    versions.value = []
  } finally {
    versionsLoading.value = false
  }
}

/** 装配体才带 assembly.json；读到就给出「打开装配台」入口与零件数 */
async function detectAssembly(id: string): Promise<void> {
  try {
    const text = await api.sidecarText(id, ASSEMBLY_SIDECAR)
    if (props.id !== id) return
    assemblyParts.value = text ? parseAssembly(text).length : 0
  } catch {
    assemblyParts.value = 0
  }
}

function openAssembly(): void {
  void router.push({ name: 'assembly', query: { from: props.id } })
}

/** 就地更新 / 回滚之后重载当前模型，并把版本记录刷新一遍 */
async function reloadModel(id: string): Promise<void> {
  try {
    const record = await readAssetFile(id)
    if (!record) throw new Error('该资产的源文件在磁盘上已丢失')
    const object = await loadModelFromBlob(record.blob, record.meta.format, {
      assetId: record.meta.id,
      hasSidecars: record.meta.hasSidecars,
    })
    if (props.id !== id) {
      disposeObject(object)
      return
    }
    // 新模型解析好再换：中间不把 model 置空，代码页签里的运行结果不会因为重载被卸掉
    const previous = model.value
    model.value = object
    if (previous) window.setTimeout(() => disposeObject(previous), 0)
    loadError.value = ''
  } catch (error) {
    loadError.value = readError(error)
  }
}

async function handleUpdated(updated: AssetMeta): Promise<void> {
  asset.value = updated
  await loadVersions(updated.id)
  await detectAssembly(updated.id)
  await reloadModel(updated.id)
}

function versionUrl(version: number): string {
  return asset.value ? api.versionFileUrl(asset.value.id, version) : '#'
}

async function doRestore(): Promise<void> {
  const current = asset.value
  const target = restoreTarget.value
  restoreTarget.value = null
  if (!current || !target) return
  restoring.value = true
  try {
    const updated = await restoreAssetVersion(current.id, target)
    await handleUpdated(updated)
    // 换了模型，缩略图已经不代表它了：顺手重拍一张，失败也不影响这次回滚
    void regenerateThumbnail(updated.id)
  } catch (error) {
    pushToast(`回滚失败：${readError(error)}`, 'error')
  } finally {
    restoring.value = false
  }
}

async function load(id: string): Promise<void> {
  loading.value = true
  loadError.value = ''
  disposeModel()
  await initLibrary()

  const meta = getAsset(id)
  if (!meta) {
    asset.value = null
    loadError.value = 'not-found'
    loading.value = false
    return
  }

  asset.value = meta
  resetDraft(meta)
  assemblyParts.value = 0
  void loadVersions(id)
  void detectAssembly(id)

  try {
    const record = await readAssetFile(id)
    if (!record) throw new Error('该资产的源文件在磁盘上已丢失')
    const object = await loadModelFromBlob(record.blob, record.meta.format, {
      assetId: record.meta.id,
      hasSidecars: record.meta.hasSidecars,
    })
    if (props.id !== id) {
      disposeObject(object)
      return
    }
    model.value = object
  } catch (error) {
    loadError.value = readError(error)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  const current = asset.value
  if (!current) return
  const name = draft.name.trim()
  if (!name) {
    pushToast('名称不能为空', 'error')
    return
  }
  saving.value = true
  try {
    const updated = await saveAssetMeta({
      id: current.id,
      name,
      category: draft.category.trim() || '未分类',
      tags: draft.tagsText
        .split(/[,，]+/)
        .map((tag) => tag.trim())
        .filter(Boolean)
        .slice(0, 12),
      description: draft.description.trim(),
    })
    if (updated) {
      asset.value = updated
      resetDraft(updated)
      pushToast('资产信息已保存', 'success')
    }
  } catch (error) {
    pushToast(`保存失败：${readError(error)}`, 'error')
  } finally {
    saving.value = false
  }
}

/** AI 分类只给建议，不直接改用户已保存的分类；采纳后再走「保存更改」。 */
async function classifyWithAI(): Promise<void> {
  const current = asset.value
  if (!current || classifying.value) return
  classifying.value = true
  try {
    const updated = await classifyAssetMeta(current.id)
    if (props.id !== current.id) return
    asset.value = updated
    const ai = updated.ai
    if (!ai) {
      pushToast('AI 没有返回分类结果', 'error')
      return
    }
    pushToast(
      `AI 建议：${ai.category || '未分类'} · ${Math.round((ai.confidence || 0) * 100)}%${aiProviderLabel(ai.provider)}`,
      'success',
    )
  } catch (error) {
    pushToast(`AI 分类失败：${readError(error)}`, 'error')
  } finally {
    classifying.value = false
  }
}

function aiProviderLabel(provider?: string): string {
  if (!provider) return ''
  if (provider.startsWith('ollama/')) return ` · Ollama ${provider.slice('ollama/'.length)}`
  if (provider === 'local-rules') return ' · 本地规则兜底'
  return ` · ${provider}`
}

function applyAISuggestion(): void {
  const suggestion = asset.value?.ai
  if (!suggestion) return
  draft.category = suggestion.category || draft.category
  const existing = draft.tagsText
    .split(/[,，]+/)
    .map((tag) => tag.trim())
    .filter(Boolean)
  const merged = Array.from(new Set([...existing, ...suggestion.tags]))
    .filter(Boolean)
    .slice(0, 12)
  draft.tagsText = merged.join(', ')
  pushToast('AI 建议已填入，点「保存更改」生效', 'info')
}

async function exportSource(): Promise<void> {
  const current = asset.value
  if (!current) return
  try {
    const record = await readAssetFile(current.id)
    if (!record) {
      pushToast('源文件已丢失，无法导出', 'error')
      return
    }
    downloadBlob(record.blob, current.fileName)
    pushToast('已导出源文件', 'success')
  } catch (error) {
    pushToast(`导出失败：${readError(error)}`, 'error')
  }
}

async function rebuildThumb(): Promise<void> {
  if (!asset.value) return
  await regenerateThumbnail(asset.value.id)
}

async function confirmDelete(): Promise<void> {
  const current = asset.value
  confirmOpen.value = false
  if (!current) return
  await removeAssets([current.id])
  void router.push({ name: 'library' })
}

function goBack(): void {
  void router.push({ name: 'library' })
}

watch(
  () => props.id,
  (id) => {
    if (id) void load(id)
  },
  { immediate: true },
)

onBeforeUnmount(disposeModel)
</script>

<template>
  <section class="detail">
    <header class="detail-head">
      <button class="btn btn-ghost back" type="button" @click="goBack">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M10 3 5 8l5 5" />
        </svg>
        资产库
      </button>

      <div class="title">
        <p class="eyebrow mono">INSPECTOR / 检视台</p>
        <h1>{{ asset?.name ?? '模型检视' }}</h1>
      </div>

      <div v-if="asset" class="head-actions">
        <div class="mode-switch" role="tablist" aria-label="检视台模式">
          <button
            type="button"
            role="tab"
            id="tab-model"
            data-mode="model"
            aria-controls="inspector-viewport"
            :tabindex="viewMode === 'model' ? 0 : -1"
            :aria-selected="viewMode === 'model'"
            :class="{ 'is-active': viewMode === 'model' }"
            @click="viewMode = 'model'"
            @keydown="onTabKey($event, 'model')"
          >
            模型
          </button>
          <button
            type="button"
            role="tab"
            id="tab-realistic"
            data-mode="realistic"
            aria-controls="inspector-viewport"
            :tabindex="viewMode === 'realistic' ? 0 : -1"
            :aria-selected="viewMode === 'realistic'"
            :class="{ 'is-active': viewMode === 'realistic' }"
            @click="viewMode = 'realistic'"
            @keydown="onTabKey($event, 'realistic')"
          >
            实景
          </button>
          <button
            type="button"
            role="tab"
            id="tab-code"
            data-mode="code"
            aria-controls="inspector-viewport"
            :tabindex="viewMode === 'code' ? 0 : -1"
            :aria-selected="viewMode === 'code'"
            :class="{ 'is-active': viewMode === 'code' }"
            @click="viewMode = 'code'"
            @keydown="onTabKey($event, 'code')"
          >
            代码
          </button>
        </div>
        <span class="chip is-active mono">{{ asset.format }}</span>
        <span class="chip mono" :title="`当前是第 ${asset.version ?? 1} 版`">v{{ asset.version ?? 1 }}</span>
        <button
          v-if="assemblyParts > 0"
          class="btn"
          type="button"
          :title="`由装配台合成，共 ${assemblyParts} 个零件`"
          @click="openAssembly"
        >
          打开装配台 · {{ assemblyParts }}
        </button>
        <button class="btn" type="button" @click="exportSource">导出源文件</button>
        <button class="btn btn-danger" type="button" @click="confirmOpen = true">删除</button>
      </div>
    </header>

    <div v-if="loadError === 'not-found'" class="fallback">
      <p class="mono state-code">NOT FOUND</p>
      <h2>找不到这个资产</h2>
      <p>它可能已被删除，或者链接中的编号不正确。</p>
      <button class="btn btn-primary" type="button" @click="goBack">返回资产库</button>
    </div>

    <div v-else-if="asset" class="body" :class="{ 'is-code': viewMode === 'code' }">
      <div
        id="inspector-viewport"
        class="viewport"
        :class="{ 'is-code': viewMode === 'code' }"
        role="tabpanel"
        :aria-labelledby="`tab-${viewMode}`"
      >
        <template v-if="model">
          <SandLab
            v-if="viewMode === 'code'"
            :model="model"
            :asset-name="asset.name"
            :asset-id="asset.id"
            derive-on-mount
            @updated="handleUpdated"
          />
          <ModelViewer
            v-else
            :model="model"
            :asset-name="asset.name"
            :mode="viewMode === 'model' ? 'model' : 'realistic'"
          />
        </template>
        <div v-else class="viewport-state">
          <p class="mono">{{ loadError ? 'LOAD ERROR' : 'LOADING MODEL' }}</p>
          <h3>{{ loadError || '正在解析模型几何体' }}</h3>
          <p v-if="!loadError" class="muted">大文件首次解析需要几秒</p>
        </div>
      </div>

      <aside class="inspector">
        <section class="panel">
          <h2 class="panel-title">几何统计</h2>
          <dl class="stat-grid">
            <div>
              <dt>三角面</dt>
              <dd class="mono">{{ formatCount(asset.stats.triangles) }}</dd>
            </div>
            <div>
              <dt>顶点</dt>
              <dd class="mono">{{ formatCount(asset.stats.vertices) }}</dd>
            </div>
            <div>
              <dt>网格</dt>
              <dd class="mono">{{ asset.stats.meshes }}</dd>
            </div>
            <div>
              <dt>材质</dt>
              <dd class="mono">{{ asset.stats.materials }}</dd>
            </div>
          </dl>
          <div class="dimension">
            <span class="label">包围尺寸</span>
            <span class="mono">{{ formatDimensions(asset.stats.dimensions) }} {{ dimensionUnitLabel(asset.format) }}</span>
          </div>
        </section>

        <section class="panel">
          <h2 class="panel-title">文件</h2>
          <dl class="file-list">
            <div>
              <dt>源文件</dt>
              <dd class="mono ellipsis" :title="asset.fileName">{{ asset.fileName }}</dd>
            </div>
            <div>
              <dt>体积</dt>
              <dd class="mono">{{ formatBytes(asset.fileSize) }}</dd>
            </div>
            <div>
              <dt>导入</dt>
              <dd class="mono">{{ formatDate(asset.createdAt) }}</dd>
            </div>
            <div>
              <dt>更新</dt>
              <dd class="mono">{{ formatDate(asset.updatedAt) }}</dd>
            </div>
            <div v-if="asset.sourcePath">
              <dt>扫描来源</dt>
              <dd class="mono ellipsis" :title="asset.sourcePath">{{ asset.sourcePath }}</dd>
            </div>
            <div v-if="asset.hasSidecars">
              <dt>外部资源</dt>
              <dd>已随模型入库（.bin / 贴图 / .mtl）</dd>
            </div>
          </dl>
        </section>

        <section class="panel">
          <h2 class="panel-title">版本记录</h2>
          <p class="panel-note">
            改完模型可以就地更新（在「代码」页签里跑完点「更新当前资产」），旧文件不会被删，而是留在这里。
          </p>
          <dl class="file-list">
            <div>
              <dt>当前</dt>
              <dd class="mono">
                v{{ asset.version ?? 1 }} · {{ formatBytes(asset.fileSize) }}
              </dd>
            </div>
          </dl>
          <ol v-if="versions.length" class="version-list">
            <li v-for="item in versions" :key="item.version">
              <div class="version-main">
                <span class="mono version-no">v{{ item.version }}</span>
                <span class="version-meta mono">{{ formatBytes(item.fileSize) }} · {{ formatDate(item.replacedAt) }}</span>
              </div>
              <div class="version-actions">
                <a class="link" :href="versionUrl(item.version)" download>下载</a>
                <button
                  class="link"
                  type="button"
                  :disabled="restoring"
                  @click="restoreTarget = item.version"
                >
                  回滚到此版
                </button>
              </div>
            </li>
          </ol>
          <p v-else class="panel-note">{{ versionsLoading ? '正在读版本记录…' : '还没有被替换下来的旧版本。' }}</p>
        </section>

        <section class="panel">
          <h2 class="panel-title">资产信息</h2>

          <label class="field-group">
            <span class="label">名称</span>
            <input v-model="draft.name" class="field" type="text" maxlength="64" />
          </label>

          <label class="field-group">
            <span class="label">分类</span>
            <input v-model="draft.category" class="field" type="text" list="category-options" maxlength="32" />
            <datalist id="category-options">
              <option value="几何体"></option>
              <option value="机械"></option>
              <option value="自然"></option>
              <option value="角色"></option>
              <option value="场景"></option>
              <option value="装配"></option>
              <option value="代码生成"></option>
              <option value="未分类"></option>
            </datalist>
          </label>

          <label class="field-group">
            <span class="label">标签（逗号分隔）</span>
            <input v-model="draft.tagsText" class="field" type="text" placeholder="低模, 硬表面, PBR" />
          </label>

          <div class="ai-box">
            <div class="ai-head">
              <span class="ai-badge">AI</span>
              <span class="ai-title">智能分类</span>
            </div>
            <p class="panel-note ai-note">
              用本机 AI 运行时读名称、描述、标签、几何与生成源码，只给建议，不自动改正式分类；批量分析在分类工作台，密钥配置在设置。
            </p>
            <div class="ai-actions">
              <button class="btn" type="button" :disabled="classifying" @click="classifyWithAI">
                {{ classifying ? '分析中…' : '让 AI 分析' }}
              </button>
              <button class="btn" type="button" @click="router.push('/classify')">分类工作台</button>
              <span v-if="classifying" class="ai-status">首次加载本地模型约需 1 分钟</span>
            </div>
            <div v-if="asset?.ai" class="ai-result">
              <div class="ai-result-line">
                <span class="ai-category">{{ asset.ai.category || '未分类' }}</span>
                <span class="ai-conf">{{ Math.round((asset.ai.confidence || 0) * 100) }}%</span>
                <span class="ai-provider">{{ aiProviderLabel(asset.ai.provider) }}</span>
              </div>
              <div v-if="asset.ai.tags.length" class="ai-tags">
                <span v-for="tag in asset.ai.tags" :key="tag" class="ai-tag">{{ tag }}</span>
              </div>
              <button class="btn" type="button" @click="applyAISuggestion">采纳建议</button>
            </div>
            <p v-else class="panel-note ai-empty">还没有 AI 分类结果。</p>
          </div>

          <label class="field-group">
            <span class="label">描述</span>
            <textarea v-model="draft.description" class="field" rows="3" placeholder="记录这个资产的用途、来源或注意事项"></textarea>
          </label>

          <div class="panel-actions">
            <button class="btn btn-primary" type="button" :disabled="!dirty || saving" @click="save">
              {{ saving ? '保存中…' : '保存更改' }}
            </button>
            <button class="btn" type="button" @click="resetDraft(asset)">还原</button>
          </div>
        </section>

        <section class="panel">
          <h2 class="panel-title">缩略图</h2>
          <p class="panel-note">用当前模型重新离屏渲染一张列表缩略图。</p>
          <button class="btn" type="button" @click="rebuildThumb">重新渲染缩略图</button>
        </section>

        <section class="panel">
          <h2 class="panel-title">格式转换</h2>
          <p class="panel-note">
            把当前模型导出成 GLB / GLTF / OBJ / STL，作为新资产入库，原件保留不动。
          </p>
          <button class="btn" type="button" @click="convertOpen = true">转换格式</button>
        </section>
      </aside>
    </div>

    <ConvertDialog :open="convertOpen" :ids="asset ? [asset.id] : []" @close="convertOpen = false" />

    <ConfirmDialog
      :open="restoreTarget !== null"
      title="回滚到这个版本"
      :message="`「${asset?.name ?? ''}」会回到 v${restoreTarget ?? ''} 的模型文件；现在的这一版同样会被记进版本记录，可以再回滚回来。`"
      confirm-label="回滚"
      @cancel="restoreTarget = null"
      @confirm="doRestore"
    />

    <ConfirmDialog
      :open="confirmOpen"
      title="删除这个资产"
      :message="`「${asset?.name ?? ''}」的模型文件与缩略图会从资产库与 data 目录中移除，不可恢复。`"
      confirm-label="删除"
      @cancel="confirmOpen = false"
      @confirm="confirmDelete"
    />
  </section>
</template>

<style scoped>
.detail {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--surface);
}

.detail-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 7px;
  border-bottom: 2px solid;
  border-color: #ffffff #808080 #808080 #ffffff;
  background: var(--surface);
}

.back {
  flex: none;
  padding-left: 0;
}

.title {
  min-width: 0;
}

.eyebrow {
  color: #333333;
  font-size: 9px;
  letter-spacing: 0;
}

h1 {
  margin-top: 1px;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 3px;
  margin-left: auto;
}

.mode-switch {
  display: flex;
  gap: 0;
  padding: 2px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #c0c0c0;
}

.mode-switch button {
  padding: 2px 8px;
  border: 2px solid transparent;
  border-radius: 0;
  background: transparent;
  color: #000000;
  font-family: inherit;
  font-size: 11px;
  letter-spacing: 0;
  cursor: pointer;
}

.mode-switch button:hover {
  border-color: var(--bevel-raised);
  background: #d4d0c8;
  color: #000000;
}

.mode-switch button.is-active {
  border-color: var(--bevel-sunken);
  background: #ffffff;
  color: #000080;
  font-weight: 700;
}

.viewport.is-code {
  background: var(--surface);
}

.body {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 344px;
  flex: 1;
  min-height: 0;
}

/* 代码工作台要更大的编辑区，右侧信息栏收窄 */
.body.is-code {
  grid-template-columns: minmax(0, 1fr) 296px;
}

.viewport {
  position: relative;
  min-width: 0;
  min-height: 0;
  border-right: 1px solid var(--line);
}

.viewport-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  height: 100%;
  color: var(--text-dim);
  text-align: center;
}

.viewport-state .mono {
  color: #000080;
  font-size: 10px;
  letter-spacing: 0;
}

.viewport-state h3 {
  font-size: 12px;
  max-width: 420px;
  line-height: 1.5;
}

.viewport-state .muted {
  color: #333333;
  font-size: 10.5px;
}

.inspector {
  display: flex;
  flex-direction: column;
  gap: 1px;
  overflow-y: auto;
  background: var(--line);
}

.panel {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 6px 8px 8px;
  background: var(--surface);
}

.panel-title {
  padding-bottom: 2px;
  border-bottom: 1px solid #808080;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0;
  text-transform: none;
  color: #000080;
}

.stat-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1px;
  margin: 0;
  background: #808080;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  overflow: hidden;
}

.stat-grid > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 6px;
  background: #ffffff;
}

.stat-grid dt {
  color: #333333;
  font-size: 10px;
}

.stat-grid dd {
  margin: 0;
  font-size: 13px;
  color: #000080;
}

.dimension {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 2px 0 0;
}

.dimension .label {
  margin: 0;
}

.dimension .mono {
  color: #000000;
  font-size: 11px;
}

.file-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
}

.file-list > div {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.file-list dt {
  flex: none;
  color: #333333;
  font-size: 10px;
}

.file-list dd {
  margin: 0;
  color: #000000;
  font-size: 10px;
  text-align: right;
}

.ellipsis {
  max-width: 210px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.field-group {
  display: block;
}

.field-group + .field-group {
  margin-top: 2px;
}

.field-group .label {
  margin-bottom: 2px;
}

.panel-actions {
  display: flex;
  gap: 3px;
  margin-top: 2px;
}

.panel-note {
  color: #333333;
  font-size: 10px;
  line-height: 1.5;
}

.ai-box {
  margin-top: 4px;
  padding: 4px;
  background: var(--surface);
  border: 2px solid;
  border-color: var(--bevel-sunken);
}

.ai-head {
  display: flex;
  align-items: center;
  gap: 4px;
}

.ai-badge {
  padding: 0 3px;
  color: #ffffff;
  background: var(--accent);
  font-size: 9px;
  font-weight: bold;
  line-height: 14px;
}

.ai-title {
  color: var(--accent);
  font-size: 11px;
  font-weight: bold;
}

.ai-note {
  margin: 3px 0;
}

.ai-actions {
  display: flex;
  align-items: center;
  gap: 5px;
  flex-wrap: wrap;
}

.ai-status {
  color: var(--danger);
  font-size: 10px;
}

.ai-result {
  margin-top: 4px;
  padding: 4px;
  background: #ffffff;
  border: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
}

.ai-result-line {
  display: flex;
  align-items: center;
  gap: 5px;
  flex-wrap: wrap;
}

.ai-category {
  color: var(--accent);
  font-weight: bold;
}

.ai-conf {
  color: var(--ok);
  font-size: 10px;
}

.ai-provider {
  color: #555555;
  font-size: 10px;
}

.ai-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
  margin: 3px 0;
}

.ai-tag {
  padding: 0 3px;
  background: var(--surface);
  border: 1px solid #808080;
  font-size: 10px;
}

.ai-empty {
  margin-top: 3px;
}

.version-list {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin: 0;
  padding: 0;
  list-style: none;
  background: #808080;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  overflow: hidden;
}

.version-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 3px 5px;
  background: #ffffff;
}

.version-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.version-no {
  color: #000080;
  font-size: 11px;
}

.version-meta {
  color: #333333;
  font-size: 9.5px;
}

.version-actions {
  display: flex;
  gap: 6px;
  flex: none;
}

.version-actions .link {
  font-size: 10px;
}

.fallback {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  max-width: 420px;
  margin: 24px auto 0;
  padding: 8px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
}

.state-code {
  color: #800000;
  font-size: 10px;
  letter-spacing: 0;
}

.fallback h2 {
  font-size: 18px;
}

.fallback p {
  color: var(--text-dim);
  font-size: 12.5px;
}

@media (max-width: 1000px) {
  .body {
    grid-template-columns: 1fr;
    grid-template-rows: 360px minmax(0, 1fr);
    overflow-y: auto;
  }

  .viewport {
    border-right: none;
    border-bottom: 1px solid var(--line);
  }

  /* 窄屏下代码模式优先给编辑区，信息栏让位 */
  .body.is-code {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr);
    overflow: hidden;
  }

  .body.is-code .viewport {
    border-bottom: none;
  }

  .body.is-code .inspector {
    display: none;
  }

  .head-actions .chip {
    display: none;
  }
}
</style>
