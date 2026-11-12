<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { formatBytes } from '../lib/format'
import { clearAllThumbAttempts } from '../lib/thumb'
import { defaultViewMode, setDefaultViewMode, setThumbAutoEnabled, thumbAutoEnabled, type LibraryViewMode } from '../lib/prefs'
import { assetCount, assets, backendOnline, initLibrary, totalBytes } from '../stores/library'
import {
  aiConfig,
  aiConfigError,
  aiConfigLoading,
  aiConfigSaving,
  aiConfigTesting,
  aiTestResult,
  clearAITestResult,
  loadAIConfig,
  saveAIConfig,
  testAIConfig,
} from '../stores/ai'
import { pushToast } from '../stores/toast'
import { AI_PROVIDER_PRESETS, aiProviderNeedsKey, aiProviderPreset } from '../lib/aiPresets'
import type { AIConfig, AIProvider } from '../types'

type SettingsTab = 'general' | 'model' | 'storage' | 'about'

const route = useRoute()
const router = useRouter()

function queryTab(): SettingsTab {
  const value = route.query.tab
  const text = Array.isArray(value) ? value[0] : value
  if (text === 'model' || text === 'storage' || text === 'about') return text
  return 'general'
}

const activeTab = ref<SettingsTab>(queryTab())
watch(
  () => route.query.tab,
  () => {
    activeTab.value = queryTab()
  },
)

const tabs: { id: SettingsTab; index: string; label: string; note: string }[] = [
  { id: 'general', index: '01', label: '常规', note: '视图 / 缩略图' },
  { id: 'model', index: '02', label: '模型配置', note: '本机 / 云端 10 类' },
  { id: 'storage', index: '03', label: '存储与数据', note: '本机 SQLite' },
  { id: 'about', index: '04', label: '关于', note: '版本与边界' },
]

function selectTab(tab: SettingsTab): void {
  activeTab.value = tab
  void router.replace({ path: '/settings', query: { tab } })
}

const viewMode = ref<LibraryViewMode>(defaultViewMode())
const autoThumb = ref(thumbAutoEnabled())

function applyViewMode(): void {
  setDefaultViewMode(viewMode.value)
  pushToast('默认资产视图已保存', 'success')
}

function applyThumbAuto(): void {
  setThumbAutoEnabled(autoThumb.value)
  pushToast(autoThumb.value ? '已开启自动补缩略图' : '已关闭自动补缩略图', 'success')
}

function clearThumbFailures(): void {
  clearAllThumbAttempts()
  pushToast('已清空本浏览器的缩略图失败计数', 'success')
}

const provider = ref<AIProvider>('ollama')
const endpoint = ref('')
const model = ref('')
const apiKey = ref('')
const clearKey = ref(false)
const localError = ref('')

const providerPreset = computed(() => aiProviderPreset(provider.value))
const providerOptions = AI_PROVIDER_PRESETS
const endpointPlaceholder = computed(() => providerPreset.value?.endpoint || 'https://your-host/v1')
const modelPlaceholder = computed(() => providerPreset.value?.model || 'your-model')

const saving = aiConfigSaving
const testing = aiConfigTesting
const loading = aiConfigLoading
const config = aiConfig
const testResult = aiTestResult
const configError = aiConfigError

function syncForm(value: AIConfig | null): void {
  if (!value) return
  if (value.provider) provider.value = value.provider
  endpoint.value = value.endpoint
  model.value = value.model
}

watch(aiConfig, (value) => syncForm(value), { immediate: true })

onMounted(() => {
  void initLibrary()
  void loadAIConfig().then((value) => syncForm(value))
})

function changeProvider(): void {
  clearAITestResult()
  localError.value = ''
  const preset = aiProviderPreset(provider.value)
  if (!preset || !preset.endpoint) return
  endpoint.value = preset.endpoint
  model.value = preset.model
}

function fillPreset(): void {
  changeProvider()
}

const keyPlaceholder = computed(() => {
  if (clearKey.value) return '保存后清除已保存的密钥'
  if (config.value?.hasKey) return `已保存 ${config.value.keyHint ?? '••••'}，留空不修改`
  return aiProviderNeedsKey(provider.value) ? '需要该服务的 API Key，只保存在服务端' : '这个 provider 通常不需要密钥'
})

function configPatch() {
  return {
    provider: provider.value,
    endpoint: endpoint.value.trim(),
    model: model.value.trim(),
    ...(apiKey.value.trim() ? { apiKey: apiKey.value.trim() } : {}),
    ...(clearKey.value ? { clearKey: true } : {}),
  }
}

async function saveConfig(): Promise<void> {
  localError.value = ''
  if (!endpoint.value.trim().startsWith('http://') && !endpoint.value.trim().startsWith('https://')) {
    localError.value = 'Endpoint 必须以 http:// 或 https:// 开头'
    return
  }
  if (!model.value.trim()) {
    localError.value = '模型名不能为空'
    return
  }
  const saved = await saveAIConfig(configPatch())
  if (!saved) {
    localError.value = configError.value || '保存失败'
    return
  }
  syncForm(saved)
  apiKey.value = ''
  clearKey.value = false
  clearAITestResult()
  pushToast('AI 配置已保存', 'success')
  void loadAIConfig(true)
}

async function probeConfig(): Promise<void> {
  localError.value = ''
  clearAITestResult()
  const result = await testAIConfig(configPatch())
  if (!result) {
    localError.value = configError.value || '测试失败'
    return
  }
  if (result.ok) pushToast(result.message, 'success')
  else pushToast(result.message, 'error')
}

function clearSavedKey(): void {
  apiKey.value = ''
  clearKey.value = true
  clearAITestResult()
}
</script>

<template>
  <section class="settings-page">
    <header class="page-head">
      <div>
        <p class="eyebrow mono">SETTINGS / 设置</p>
        <h1>设置</h1>
        <p class="sub">常规界面偏好与 AI 模型配置 · 配置保存在本机</p>
      </div>
      <span class="mono head-note">03 / 设置</span>
    </header>

    <div class="settings-shell">
      <nav class="settings-nav" aria-label="设置分类">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          class="settings-nav-item"
          type="button"
          :class="{ 'is-active': activeTab === tab.id }"
          @click="selectTab(tab.id)"
        >
          <span class="nav-index mono">{{ tab.index }}</span>
          <span class="nav-label">{{ tab.label }}</span>
          <span class="nav-note mono">{{ tab.note }}</span>
        </button>
      </nav>

      <main class="settings-content">
        <section v-if="activeTab === 'general'" class="panel">
          <div class="panel-head">
            <h2>常规</h2>
            <span class="mono panel-note">只影响当前浏览器</span>
          </div>
          <div class="setting-list">
            <div class="setting-row">
              <div class="setting-copy">
                <strong>默认资产视图</strong>
                <span>下次进入资产库时用网格还是列表；不会改变资产数据。</span>
              </div>
              <div class="segmented" role="group" aria-label="默认资产视图">
                <button class="seg-btn" type="button" :class="{ 'is-active': viewMode === 'grid' }" @click="viewMode = 'grid'; applyViewMode()">网格</button>
                <button class="seg-btn" type="button" :class="{ 'is-active': viewMode === 'list' }" @click="viewMode = 'list'; applyViewMode()">列表</button>
              </div>
            </div>

            <div class="setting-row">
              <div class="setting-copy">
                <strong>自动补缩略图</strong>
                <span>进入资产库时，为缺图的资产自动离屏渲染；关闭后只在卡片上手动重试。</span>
              </div>
              <label class="switch-row">
                <input v-model="autoThumb" type="checkbox" @change="applyThumbAuto" />
                <span class="mono">{{ autoThumb ? '开启' : '关闭' }}</span>
              </label>
            </div>

            <div class="setting-row">
              <div class="setting-copy">
                <strong>缩略图失败计数</strong>
                <span>连续失败 3 次会停止自动重试；清空后下次进入资产库会重新尝试。</span>
              </div>
              <button class="btn" type="button" @click="clearThumbFailures">清空失败计数</button>
            </div>

            <div class="setting-row">
              <div class="setting-copy">
                <strong>界面风格</strong>
                <span>Dev-C++ / Win9x 经典灰，全站固定；不提供现代圆角或渐变皮肤。</span>
              </div>
              <span class="mono static-value">经典灰 · 固定</span>
            </div>

            <div class="setting-row">
              <div class="setting-copy">
                <strong>模型配置</strong>
                <span>Ollama、OpenAI、DeepSeek、Kimi、智谱、通义、SiliconFlow、Groq、LM Studio 与自定义兼容接口。</span>
              </div>
              <button class="btn" type="button" @click="selectTab('model')">打开模型配置</button>
            </div>
          </div>
          <p class="panel-foot mono">这些偏好存在浏览器 localStorage；换浏览器或清缓存会恢复默认。</p>
        </section>

        <section v-else-if="activeTab === 'model'" class="panel">
          <div class="panel-head">
            <h2>模型配置</h2>
            <span class="mono panel-note">{{ config?.label || '未配置' }}</span>
          </div>

          <div v-if="configError || localError" class="inline-error" role="alert">{{ localError || configError }}</div>

          <div class="config-grid">
            <label class="field-wrap">
              <span class="field-label">Provider</span>
              <select v-model="provider" class="field" @change="changeProvider">
                <option v-for="item in providerOptions" :key="item.id" :value="item.id">{{ item.label }}</option>
              </select>
              <span class="field-note">{{ providerPreset?.hint || '选择 provider 后可参考推荐值，端点与模型仍可手改。' }}</span>
            </label>
            <label class="field-wrap">
              <span class="field-label">模型</span>
              <input v-model="model" class="field" spellcheck="false" :placeholder="modelPlaceholder" />
            </label>
            <label class="field-wrap wide">
              <span class="field-label">Endpoint</span>
              <input v-model="endpoint" class="field" spellcheck="false" :placeholder="endpointPlaceholder" />
            </label>
            <label class="field-wrap wide key-field">
              <span class="field-label">API Key</span>
              <span class="key-row">
                <input
                  v-model="apiKey"
                  class="field"
                  type="password"
                  autocomplete="off"
                  spellcheck="false"
                  :disabled="clearKey"
                  :placeholder="keyPlaceholder"
                />
                <button v-if="config?.hasKey && !clearKey" class="btn btn-sm" type="button" @click="clearSavedKey">清除</button>
                <button v-if="clearKey" class="btn btn-sm" type="button" @click="clearKey = false">撤销</button>
              </span>
            </label>
          </div>

          <div class="config-actions">
            <button class="btn btn-primary" type="button" :disabled="saving || loading" @click="saveConfig">
              {{ saving ? '保存中…' : '保存配置' }}
            </button>
            <button class="btn" type="button" :disabled="testing || loading" @click="probeConfig">
              {{ testing ? '测试中…' : '测试连接' }}
            </button>
            <button class="btn" type="button" :disabled="testing || loading" @click="fillPreset">使用推荐值</button>
            <span class="mono config-hint">
              {{ config?.hasKey ? `密钥 ${config.keyHint}` : '未保存密钥' }} · {{ endpoint || '未填 Endpoint' }}
            </span>
          </div>

          <div v-if="testResult" class="test-result" :class="{ 'is-bad': !testResult.ok }">
            <strong>{{ testResult.ok ? '连接成功' : '连接失败' }}</strong>
            <span>{{ testResult.message }}</span>
            <div v-if="testResult.models?.length" class="model-chips">
              <button v-for="item in testResult.models.slice(0, 12)" :key="item" class="chip" type="button" @click="model = item">
                {{ item }}
              </button>
            </div>
          </div>

          <div class="model-links">
            <RouterLink to="/ai" class="btn">打开 AI 助手</RouterLink>
            <RouterLink to="/classify" class="btn">打开分类工作台</RouterLink>
          </div>
          <p class="panel-foot mono">
            密钥写入服务端 data/ai-config.json，仅存本机数据目录；GET 只返回尾号，分类失败退回本地规则。
          </p>
        </section>

        <section v-else-if="activeTab === 'storage'" class="panel">
          <div class="panel-head">
            <h2>存储与数据</h2>
            <span class="mono panel-note">{{ backendOnline ? 'GO 服务 · 已连接' : 'GO 服务 · 未连接' }}</span>
          </div>
          <div class="storage-grid">
            <div class="storage-row">
              <span>数据目录</span>
              <strong class="mono">server/data</strong>
            </div>
            <div class="storage-row">
              <span>资产数量</span>
              <strong class="mono">{{ assetCount }}</strong>
            </div>
            <div class="storage-row">
              <span>主文件合计</span>
              <strong class="mono">{{ formatBytes(totalBytes) }}</strong>
            </div>
            <div class="storage-row">
              <span>数据库</span>
              <strong class="mono">SQLite · assets / jobs / versions</strong>
            </div>
          </div>
          <p class="storage-note">
            模型、缩略图、版本、外部资源都在同一个 data 目录里；换设备时整个目录拷贝即可。这个设置页不做备份或迁移按钮，避免误复制正在写入的 SQLite 文件。
          </p>
          <div class="storage-assets mono">当前载入 {{ assets.length }} 条资产记录</div>
        </section>

        <section v-else class="panel">
          <div class="panel-head">
            <h2>关于</h2>
            <span class="mono panel-note">本地单用户工具</span>
          </div>
          <div class="about-copy">
            <p><strong>ASSET STUDIO</strong> 是本机三维资产检视台：GLB / GLTF / OBJ / STL / FBX / PMX / .blend 入库，Three.js 检视、代码建模、装配与 AI 辅助都走本地服务。</p>
            <p>技术栈：Vue 3 + TypeScript + Three.js，后端 Go + SQLite，PMX / .blend 转换依赖本机 Blender。</p>
            <p>数据只在本机，没有账号体系与多人共享；AI 运行时支持 Ollama、OpenAI、DeepSeek、Kimi、智谱、通义、SiliconFlow、Groq、LM Studio 和自定义兼容接口，密钥只存在服务端 data 目录。</p>
            <p class="mono">界面：Dev-C++ / Win9x 经典灰 · 没有现代圆角与渐变。</p>
          </div>
          <div class="about-links">
            <RouterLink to="/" class="btn">回到资产库</RouterLink>
            <RouterLink to="/settings?tab=model" class="btn">查看模型配置</RouterLink>
          </div>
        </section>
      </main>
    </div>
  </section>
</template>

<style scoped>
.settings-page {
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

.head-note {
  flex: none;
  color: #666666;
  font-size: 9px;
}

.settings-shell {
  display: grid;
  grid-template-columns: 168px minmax(0, 1fr);
  gap: 8px;
  align-items: start;
  margin-top: 8px;
}

.settings-nav {
  border: 2px solid;
  border-color: var(--bevel-raised);
  background: var(--surface);
  padding: 3px;
}

.settings-nav-item {
  display: flex;
  align-items: center;
  gap: 5px;
  width: 100%;
  padding: 4px;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: #000000;
  font-size: 10px;
  text-align: left;
  cursor: pointer;
}

.settings-nav-item:hover {
  background: #c0c0c0;
}

.settings-nav-item.is-active {
  background: #000080;
  color: #ffffff;
}

.settings-nav-item.is-active .nav-index,
.settings-nav-item.is-active .nav-note {
  color: #d4d0c8;
}

.nav-index {
  width: 18px;
  flex: none;
  color: #666666;
  font-size: 8px;
}

.nav-label {
  flex: none;
}

.nav-note {
  margin-left: auto;
  color: #666666;
  font-size: 8px;
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings-content {
  min-width: 0;
}

.panel {
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

.setting-list {
  padding: 2px 6px;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 0;
  border-bottom: 1px solid #d4d0c8;
}

.setting-row:last-child {
  border-bottom: 0;
}

.setting-copy {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.setting-copy strong {
  font-size: 10px;
}

.setting-copy span {
  color: #333333;
  font-size: 9px;
  line-height: 1.45;
}

.segmented {
  display: flex;
  flex: none;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
}

.seg-btn {
  min-width: 52px;
  padding: 2px 6px;
  border: 0;
  border-right: 1px solid #808080;
  background: var(--surface);
  color: #000000;
  font-size: 9px;
  cursor: pointer;
}

.seg-btn:last-child {
  border-right: 0;
}

.seg-btn.is-active {
  background: #000080;
  color: #ffffff;
}

.switch-row {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
  color: #333333;
  font-size: 9px;
}

.static-value {
  flex: none;
  color: #333333;
  font-size: 9px;
}

.panel-foot {
  margin: 0;
  padding: 5px 6px;
  border-top: 1px solid #808080;
  color: #333333;
  font-size: 9px;
  line-height: 1.45;
}

.inline-error {
  margin: 6px;
  padding: 3px 6px;
  border: 1px solid #800000;
  background: #fff0f0;
  color: #800000;
  font-size: 10px;
}

.config-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 4px 6px;
  padding: 6px;
}

.field-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.field-wrap.wide {
  grid-column: 1 / -1;
}

.field-label {
  color: #333333;
  font-size: 9px;
  line-height: 1.2;
}

.field-note {
  color: #555555;
  font-size: 8px;
  line-height: 1.35;
}

.key-row {
  display: flex;
  gap: 3px;
  min-width: 0;
}

.key-row .field {
  min-width: 0;
}

.config-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 6px 6px;
}

.config-hint {
  overflow: hidden;
  color: #333333;
  font-size: 9px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.test-result {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin: 0 6px 6px;
  padding: 4px 6px;
  border: 1px solid #006400;
  background: #f2fff2;
  color: #004d00;
  font-size: 10px;
}

.test-result.is-bad {
  border-color: #800000;
  background: #fff0f0;
  color: #800000;
}

.model-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 3px;
  margin-top: 2px;
}

.model-links {
  display: flex;
  gap: 4px;
  padding: 0 6px 5px;
}

.model-links .btn {
  text-decoration: none;
}

.storage-grid {
  display: grid;
  gap: 0;
  padding: 4px 6px;
}

.storage-row {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 4px 0;
  border-bottom: 1px solid #d4d0c8;
  font-size: 10px;
}

.storage-row:last-child {
  border-bottom: 0;
}

.storage-row span {
  color: #333333;
}

.storage-row strong {
  font-size: 9px;
  text-align: right;
  overflow-wrap: anywhere;
}

.storage-note {
  margin: 0 6px;
  padding: 5px 6px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
  color: #333333;
  font-size: 9px;
  line-height: 1.5;
}

.storage-assets {
  padding: 5px 6px;
  color: #666666;
  font-size: 8px;
  text-align: right;
}

.about-copy {
  padding: 7px 8px;
  font-size: 10px;
  line-height: 1.65;
}

.about-copy p {
  margin: 0 0 5px;
}

.about-copy p:last-child {
  margin-bottom: 0;
  color: #333333;
  font-size: 9px;
}

.about-links {
  display: flex;
  gap: 4px;
  padding: 0 8px 8px;
}

.about-links .btn {
  text-decoration: none;
}

@media (max-width: 620px) {
  .settings-shell {
    grid-template-columns: 1fr;
  }

  .settings-nav {
    display: flex;
    flex-wrap: wrap;
    gap: 1px;
  }

  .settings-nav-item {
    width: auto;
  }

  .nav-note {
    display: none;
  }

  .config-grid {
    grid-template-columns: 1fr;
  }

  .field-wrap.wide {
    grid-column: auto;
  }

  .head-note {
    display: none;
  }

  .config-hint {
    display: none;
  }
}
</style>
