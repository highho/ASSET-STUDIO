<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api'
import { AI_PROVIDER_PRESETS, aiProviderLabel, aiProviderNeedsKey, aiProviderPreset } from '../lib/aiPresets'
import { aiConfig, aiConfigError, loadAIConfig, saveAIConfig } from '../stores/ai'
import type { AIProvider } from '../types'

const emit = defineEmits<{ close: [] }>()

const step = ref(0)
const provider = ref<AIProvider>('ollama')
const endpoint = ref('')
const model = ref('')
const apiKey = ref('')
const testing = ref(false)
const saving = ref(false)
const error = ref('')
const testMessage = ref('')

const currentPreset = computed(() => aiProviderPreset(provider.value))
const endpointPlaceholder = computed(() => currentPreset.value?.endpoint || 'https://your-host/v1')
const modelPlaceholder = computed(() => currentPreset.value?.model || 'your-model')
const keyPlaceholder = computed(() => {
  if (aiConfig.value?.hasKey) return `已保存 ${aiConfig.value.keyHint ?? '••••'}，留空不修改`
  return aiProviderNeedsKey(provider.value) ? '粘贴 API Key，只保存在服务端' : '通常不需要密钥，可留空'
})

onMounted(async () => {
  await loadAIConfig()
  const current = aiConfig.value
  if (current?.provider) {
    provider.value = current.provider
    endpoint.value = current.endpoint
    model.value = current.model
  }
})

function usePresetValues(): void {
  const preset = currentPreset.value
  if (!preset || !preset.endpoint || !preset.model) return
  endpoint.value = preset.endpoint
  model.value = preset.model
}

function validateForm(): string {
  if (!endpoint.value.trim().startsWith('http://') && !endpoint.value.trim().startsWith('https://')) {
    return 'Endpoint 必须以 http:// 或 https:// 开头'
  }
  if (!model.value.trim()) return '模型名不能为空'
  return ''
}

function patch() {
  return {
    provider: provider.value,
    endpoint: endpoint.value.trim(),
    model: model.value.trim(),
    ...(apiKey.value.trim() ? { apiKey: apiKey.value.trim() } : {}),
  }
}

async function testConnection(): Promise<void> {
  error.value = ''
  testMessage.value = ''
  const invalid = validateForm()
  if (invalid) {
    error.value = invalid
    return
  }
  testing.value = true
  try {
    const result = await api.testAIConfig(patch())
    if (result.ok) {
      testMessage.value = result.models?.length
        ? `${result.message}：${result.models.slice(0, 4).join('、')}`
        : result.message
    } else {
      error.value = result.message || '连接失败'
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    testing.value = false
  }
}

async function finish(): Promise<void> {
  error.value = ''
  const invalid = validateForm()
  if (invalid) {
    error.value = invalid
    return
  }
  saving.value = true
  try {
    const saved = await saveAIConfig(patch())
    if (!saved) {
      error.value = aiConfigError.value || '保存配置失败'
      return
    }
    apiKey.value = ''
    testMessage.value = ''
    step.value = 2
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="onboarding-overlay" role="dialog" aria-modal="true" aria-labelledby="onboarding-title">
    <section class="onboarding-window">
      <header class="onboarding-head">
        <div>
          <p class="eyebrow mono">ONBOARDING / 首次开启</p>
          <h2 id="onboarding-title">ASSET STUDIO</h2>
        </div>
        <span class="mono onboarding-count">0{{ step + 1 }} / 03</span>
      </header>

      <div class="onboarding-progress" aria-hidden="true">
        <span v-for="index in 3" :key="index" :class="{ 'is-on': step + 1 >= index }">{{ index === 1 ? '欢迎' : index === 2 ? '模型' : '完成' }}</span>
      </div>

      <section v-if="step === 0" class="onboarding-body">
        <p class="onboarding-lead">本机三维资产检视台，先把散在硬盘里的模型收进一个可查、可装配的库。</p>
        <ul class="onboarding-list">
          <li>支持 GLB / GLTF / OBJ / STL / FBX 直接入库，PMX / .blend 交给本机 Blender 转换。</li>
          <li>资产库、详情页、装配台和 sand3d 代码新建都在本机完成，不上传云端。</li>
          <li>AI 助手可以读当前资产库上下文，做分类、格式差异和装配思路的辅助。</li>
        </ul>
        <p class="onboarding-note mono">下一步会选一个模型 provider；暂时不想配也可以先跳过，之后在「设置 → 模型配置」补上。</p>
      </section>

      <section v-else-if="step === 1" class="onboarding-body">
        <p class="onboarding-lead">先给 AI 接一个模型。填好之后可以测试连接；密钥只写进本机服务端 data 目录。</p>
        <div v-if="error" class="inline-error" role="alert">{{ error }}</div>

        <div class="onboarding-form">
          <label class="onboarding-field">
            <span class="field-label">Provider</span>
            <select v-model="provider" class="field" @change="usePresetValues">
              <option v-for="item in AI_PROVIDER_PRESETS" :key="item.id" :value="item.id">{{ item.label }}</option>
            </select>
          </label>
          <div class="onboarding-preset-row">
            <span class="mono">{{ currentPreset?.hint }}</span>
            <button class="btn btn-sm" type="button" @click="usePresetValues">使用推荐值</button>
          </div>
          <label class="onboarding-field">
            <span class="field-label">Endpoint</span>
            <input v-model="endpoint" class="field" spellcheck="false" :placeholder="endpointPlaceholder" />
          </label>
          <label class="onboarding-field">
            <span class="field-label">模型</span>
            <input v-model="model" class="field" spellcheck="false" :placeholder="modelPlaceholder" />
          </label>
          <label class="onboarding-field">
            <span class="field-label">API Key</span>
            <input
              v-model="apiKey"
              class="field"
              type="password"
              autocomplete="off"
              spellcheck="false"
              :placeholder="keyPlaceholder"
            />
          </label>
        </div>

        <div v-if="testMessage" class="onboarding-test mono">{{ testMessage }}</div>
      </section>

      <section v-else class="onboarding-body">
        <p class="onboarding-lead">配置已保存。模型：<span class="mono">{{ aiProviderLabel(aiConfig?.provider) }} · {{ aiConfig?.model || '未配置' }}</span></p>
        <ul class="onboarding-list">
          <li>资产库、分类工作台和 AI 助手都已经在同一套本地服务里。</li>
          <li>换模型可以直接在 AI 助手页头下拉选择，不需要再回来改整页配置。</li>
          <li>所有资产文件、缩略图和数据库都在本机 data 目录。</li>
        </ul>
        <p class="onboarding-note mono">之后想重新配置，随时从侧栏进入「设置 → 模型配置」。</p>
      </section>

      <footer class="onboarding-actions">
        <button v-if="step === 0" class="btn" type="button" @click="emit('close')">稍后配置</button>
        <button v-if="step === 0" class="btn btn-primary" type="button" @click="step = 1">开始</button>

        <button v-if="step === 1" class="btn" type="button" @click="step = 0">返回</button>
        <button v-if="step === 1" class="btn" type="button" @click="emit('close')">跳过</button>
        <button v-if="step === 1" class="btn" type="button" :disabled="testing || saving" @click="testConnection">
          {{ testing ? '测试中…' : '测试连接' }}
        </button>
        <button v-if="step === 1" class="btn btn-primary" type="button" :disabled="saving || testing" @click="finish">
          {{ saving ? '保存中…' : '保存并继续' }}
        </button>

        <button v-if="step === 2" class="btn btn-primary" type="button" @click="emit('close')">进入资产库</button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.onboarding-overlay {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 18px;
  background: rgba(0, 0, 0, 0.38);
}

.onboarding-window {
  width: min(560px, 100%);
  max-height: calc(100vh - 36px);
  overflow: auto;
  border: 2px solid;
  border-color: var(--bevel-raised);
  background: #c0c0c0;
  box-shadow: 4px 4px 0 rgba(0, 0, 0, 0.45);
}

.onboarding-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
  padding: 9px 10px 6px;
  border-bottom: 1px solid #808080;
}

.onboarding-head .eyebrow {
  margin: 0 0 2px;
  color: #555555;
  font-size: 9px;
}

.onboarding-head h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 700;
}

.onboarding-count {
  color: #333333;
  font-size: 9px;
  white-space: nowrap;
}

.onboarding-progress {
  display: flex;
  gap: 0;
  border-bottom: 1px solid #808080;
}

.onboarding-progress span {
  flex: 1;
  padding: 3px 6px;
  border-right: 1px solid #808080;
  color: #333333;
  font-size: 9px;
  text-align: center;
}

.onboarding-progress span:last-child {
  border-right: 0;
}

.onboarding-progress span.is-on {
  background: #000080;
  color: #ffffff;
}

.onboarding-body {
  padding: 10px;
  font-size: 10px;
  line-height: 1.55;
}

.onboarding-lead {
  margin: 0 0 7px;
  font-size: 11px;
  font-weight: 500;
}

.onboarding-list {
  margin: 0 0 7px;
  padding-left: 17px;
}

.onboarding-list li {
  margin: 2px 0;
}

.onboarding-note {
  margin: 0;
  color: #444444;
  font-size: 9px;
  line-height: 1.45;
}

.onboarding-form {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.onboarding-field {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.onboarding-preset-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  min-height: 20px;
}

.onboarding-preset-row .mono {
  min-width: 0;
  overflow: hidden;
  color: #444444;
  font-size: 8px;
  line-height: 1.35;
  text-overflow: ellipsis;
}

.onboarding-test {
  margin-top: 6px;
  padding: 3px 5px;
  border: 1px solid #808080;
  background: #dcdcdc;
  color: #004000;
  font-size: 9px;
}

.onboarding-actions {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
  padding: 7px 10px;
  border-top: 1px solid #808080;
}
</style>
