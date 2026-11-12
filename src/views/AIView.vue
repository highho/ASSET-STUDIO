<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../lib/api'
import {
  AI_CHAT_CONTEXT_LIMIT,
  AI_CHAT_MESSAGE_LIMIT,
  AI_SESSION_LIMIT,
  AI_SESSION_MESSAGE_LIMIT,
  chatSessionTime,
  chatSessionTitle,
  chatCharCount,
  loadChatSessions,
  newChatEntry,
  newChatSession,
  planChatPayload,
  saveChatSessions,
  type ChatSession,
} from '../lib/aiChat'
import { readError } from '../lib/download'
import { initLibrary } from '../stores/library'
import { aiConfig, aiConfigError, loadAIConfig, saveAIConfig } from '../stores/ai'
import { aiProviderLabel, aiProviderNeedsKey } from '../lib/aiPresets'
import { pushToast } from '../stores/toast'

const config = aiConfig
const configError = aiConfigError
const chatReady = computed(() => {
  const current = config.value
  if (!current?.provider || !current.endpoint || !current.model) return false
  return !aiProviderNeedsKey(current.provider) || Boolean(current.hasKey)
})

const selectedModel = ref('')
const modelOptions = ref<string[]>([])
const modelsLoading = ref(false)
const modelSwitching = ref(false)
const modelError = ref('')
const modelChoices = computed(() => {
  const current = config.value?.model || ''
  const list = modelOptions.value
  return current && !list.includes(current) ? [current, ...list] : list
})

watch(
  () => config.value?.model,
  (model) => {
    if (model) selectedModel.value = model
  },
  { immediate: true },
)

async function refreshModels(announce = false): Promise<void> {
  if (modelsLoading.value) return
  if (!config.value?.provider || !config.value.endpoint || !config.value.model) {
    modelOptions.value = []
    return
  }
  modelsLoading.value = true
  modelError.value = ''
  try {
    const result = await api.testAIConfig({})
    if (result.ok) {
      modelOptions.value = result.models ?? []
      if (announce) pushToast(result.message, 'success')
    } else {
      modelError.value = result.message || '无法读取模型列表'
    }
  } catch (error) {
    modelError.value = readError(error)
  } finally {
    modelsLoading.value = false
  }
}

async function applyModel(): Promise<void> {
  const target = selectedModel.value
  const current = config.value?.model || ''
  if (!target || target === current || modelSwitching.value || chatBusy.value) return
  modelSwitching.value = true
  modelError.value = ''
  try {
    const updated = await saveAIConfig({ model: target })
    if (!updated) {
      selectedModel.value = current
      modelError.value = aiConfigError.value || '保存模型失败'
      return
    }
    chatProvider.value = ''
    pushToast(`对话模型已切换为 ${updated.model}`, 'success')
  } finally {
    modelSwitching.value = false
  }
}

function getStorage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

const storedSessions = loadChatSessions(getStorage())
const sessions = ref<ChatSession[]>(storedSessions.length ? storedSessions : [newChatSession()])
const activeSessionId = ref(sessions.value[0].id)
const activeSession = computed(() => sessions.value.find((session) => session.id === activeSessionId.value) ?? sessions.value[0])
const activeMessages = computed(() => activeSession.value.messages)
const activeTitle = computed(() => chatSessionTitle(activeSession.value))
const contextPlan = computed(() => planChatPayload(activeMessages.value))
const contextPct = computed(() =>
  Math.min(100, Math.round((contextPlan.value.messages.length / AI_CHAT_CONTEXT_LIMIT) * 100)),
)
const contextWarn = computed(() => contextPlan.value.dropped > 0 || contextPlan.value.truncated > 0)
const contextNote = computed(() => {
  const plan = contextPlan.value
  if (!plan.total) return '还没有可发送的消息。'
  let note =
    plan.dropped > 0
      ? `长对话：发送时只带最近 ${AI_CHAT_CONTEXT_LIMIT} 条，更早的 ${plan.dropped} 条仍留在记录里，但模型看不到。`
      : `发送时带上本会话最近 ${plan.messages.length}/${AI_CHAT_CONTEXT_LIMIT} 条。`
  if (plan.truncated > 0) {
    note += ` 另有 ${plan.truncated} 条超长消息按 ${AI_CHAT_MESSAGE_LIMIT} 字截断后发送。`
  }
  return note
})
const sessionFull = computed(() => activeMessages.value.length + 1 >= AI_SESSION_MESSAGE_LIMIT)

const chatInput = ref('')
const chatInputEl = ref<HTMLTextAreaElement | null>(null)
const chatBusy = ref(false)
const chatError = ref('')
const chatProvider = ref('')
const chatLog = ref<HTMLElement | null>(null)
const atBottom = ref(true)
const unseen = ref(0)
const expandedIds = ref(new Set<string>())
const persistWarned = ref(false)

const inputTooLong = computed(() => chatCharCount(chatInput.value) > AI_CHAT_MESSAGE_LIMIT)
const canSend = computed(
  () =>
    chatReady.value &&
    !chatBusy.value &&
    !modelSwitching.value &&
    !sessionFull.value &&
    Boolean(chatInput.value.trim()) &&
    !inputTooLong.value,
)

const quickPrompts = [
  '概括资产库并指出缺信息项',
  '给出整理与重分类建议',
  'GLB / FBX / PMX 的差别',
  '用 sand3d 复刻资产的步骤',
]

onMounted(async () => {
  void initLibrary()
  await loadAIConfig()
  void refreshModels()
  if (!storedSessions.length) persistSessions()
  await scrollChat(true)
})

function persistSessions(): void {
  const ok = saveChatSessions(getStorage(), sessions.value)
  if (!ok && !persistWarned.value) {
    persistWarned.value = true
    pushToast('会话无法写入浏览器本地存储，只在本次打开期间保留', 'error')
  }
}

function touchSession(session: ChatSession): void {
  session.updatedAt = Date.now()
}

function titleOf(session: ChatSession): string {
  return chatSessionTitle(session)
}

function countOf(session: ChatSession): number {
  return session.messages.length
}

function timeOf(session: ChatSession): string {
  return chatSessionTime(session)
}

function newSession(): void {
  if (chatBusy.value) return
  if (sessions.value.length >= AI_SESSION_LIMIT) {
    pushToast(`最多保留 ${AI_SESSION_LIMIT} 个会话，请先删除旧会话`, 'error')
    return
  }
  const session = newChatSession()
  sessions.value.unshift(session)
  activeSessionId.value = session.id
  chatError.value = ''
  expandedIds.value.clear()
  unseen.value = 0
  persistSessions()
  void scrollChat(true)
  void focusInput()
}

function switchSession(id: string): void {
  if (chatBusy.value || id === activeSessionId.value) return
  if (!sessions.value.some((session) => session.id === id)) return
  activeSessionId.value = id
  chatError.value = ''
  expandedIds.value.clear()
  unseen.value = 0
  atBottom.value = true
  void scrollChat(true)
  void focusInput()
}

function deleteSession(id: string): void {
  if (chatBusy.value) return
  const target = sessions.value.find((session) => session.id === id)
  if (!target) return
  const message = `删除会话「${chatSessionTitle(target)}」？记录只在本浏览器，删除后无法恢复。`
  if (!window.confirm(message)) return
  sessions.value = sessions.value.filter((session) => session.id !== id)
  if (!sessions.value.length) {
    sessions.value = [newChatSession()]
  }
  if (activeSessionId.value === id || !sessions.value.some((session) => session.id === activeSessionId.value)) {
    activeSessionId.value = sessions.value[0].id
  }
  chatError.value = ''
  expandedIds.value.clear()
  unseen.value = 0
  persistSessions()
  void scrollChat(true)
}

async function focusInput(): Promise<void> {
  await nextTick()
  chatInputEl.value?.focus()
}

async function scrollChat(force = false): Promise<void> {
  await nextTick()
  const el = chatLog.value
  if (!el) return
  if (!force && !atBottom.value) return
  el.scrollTop = el.scrollHeight
  atBottom.value = true
  unseen.value = 0
}

function onLogScroll(): void {
  const el = chatLog.value
  if (!el) return
  const distance = el.scrollHeight - el.scrollTop - el.clientHeight
  atBottom.value = distance < 24
  if (atBottom.value) unseen.value = 0
}

function scrollLatest(): void {
  void scrollChat(true)
}

function isLong(message: { content: string }): boolean {
  return chatCharCount(message.content) > 700
}

function isExpanded(id: string): boolean {
  return expandedIds.value.has(id)
}

function toggleExpanded(id: string): void {
  const next = new Set(expandedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expandedIds.value = next
}

function displayContent(message: { id: string; content: string }): string {
  if (!isLong(message) || isExpanded(message.id)) return message.content
  return `${message.content.slice(0, 620).trimEnd()}…`
}

async function sendChat(text = chatInput.value): Promise<void> {
  const content = text.trim()
  if (!content || chatBusy.value) return
  if (!chatReady.value) {
    chatError.value = '当前模型还不能对话，请先到「设置 → 模型配置」保存可用的 provider 与密钥。'
    return
  }
  if (chatCharCount(content) > AI_CHAT_MESSAGE_LIMIT) {
    chatError.value = `单条消息最多 ${AI_CHAT_MESSAGE_LIMIT} 字，请拆成几条再发。`
    return
  }
  if (sessionFull.value) {
    chatError.value = `本会话已接近 ${AI_SESSION_MESSAGE_LIMIT} 条记录上限，请新建会话继续。`
    return
  }
  chatError.value = ''
  const session = activeSession.value
  session.messages.push(newChatEntry('user', content))
  touchSession(session)
  chatInput.value = ''
  chatBusy.value = true
  persistSessions()
  await scrollChat(true)
  try {
    const plan = planChatPayload(session.messages)
    const reply = await api.aiChat(plan.messages)
    session.messages.push(newChatEntry('assistant', reply.reply))
    touchSession(session)
    chatProvider.value = reply.provider
    persistSessions()
  } catch (error) {
    chatError.value = readError(error)
    pushToast(chatError.value, 'error')
  } finally {
    chatBusy.value = false
    if (atBottom.value) await scrollChat(true)
    else unseen.value += 1
  }
}

function onChatKey(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
  event.preventDefault()
  void sendChat()
}

function usePrompt(prompt: string): void {
  if (!chatReady.value || sessionFull.value) return
  void sendChat(prompt)
}
</script>

<template>
  <section class="ai-page">
    <header class="page-head">
      <div>
        <p class="eyebrow mono">AI / 助手</p>
        <h1>AI 助手</h1>
        <p class="sub">
          带着当前资产库上下文对话 · 会话只存本浏览器 · 模型每次只读最近 {{ AI_CHAT_CONTEXT_LIMIT }} 条
        </p>
      </div>
      <div class="ai-head-status">
        <div class="model-switch">
          <span class="mono model-label">模型</span>
          <select
            v-model="selectedModel"
            class="model-select"
            aria-label="对话模型"
            :disabled="chatBusy || modelSwitching || modelsLoading || !modelChoices.length"
            :title="modelError || '切换对话模型'"
            @change="applyModel"
          >
            <option v-if="!modelChoices.length" value="">{{ config?.model || '未配置' }}</option>
            <option v-for="model in modelChoices" :key="model" :value="model">{{ model }}</option>
          </select>
          <button
            class="model-refresh"
            type="button"
            :disabled="chatBusy || modelsLoading || modelSwitching"
            :title="modelsLoading ? '正在读取模型列表' : '重新读取本机模型列表'"
            @click="refreshModels(true)"
          >
            ↻
          </button>
        </div>
        <div class="status-row">
          <span class="mono">{{ aiProviderLabel(config?.provider) }}</span>
          <span class="status-pill" :class="{ 'is-ok': chatReady, 'is-bad': !chatReady }">
            {{ chatReady ? '可对话' : '需要配置' }}
          </span>
        </div>
        <span v-if="modelError" class="mono model-error" :title="modelError">模型列表读取失败 · 点 ↻ 重试</span>
      </div>
    </header>

    <div v-if="configError" class="inline-error" role="alert">{{ configError }}</div>

    <div class="ai-workspace">
      <aside class="panel sessions-panel">
        <div class="panel-head">
          <h2>会话</h2>
          <button class="btn session-new" type="button" :disabled="chatBusy" @click="newSession">新建</button>
        </div>
        <div class="session-list" role="list">
          <div
            v-for="session in sessions"
            :key="session.id"
            class="session-item"
            :class="{ 'is-active': session.id === activeSession.id }"
            role="listitem"
          >
            <button
              class="session-main"
              type="button"
              :disabled="chatBusy"
              :title="chatSessionTitle(session)"
              @click="switchSession(session.id)"
            >
              <span class="session-title">{{ titleOf(session) }}</span>
              <span class="session-meta mono">{{ countOf(session) }} 条 · {{ timeOf(session) }}</span>
            </button>
            <button
              class="session-del"
              type="button"
              :disabled="chatBusy"
              :title="`删除会话 ${chatSessionTitle(session)}`"
              @click="deleteSession(session.id)"
            >
              ×
            </button>
          </div>
        </div>
        <div class="session-foot mono">
          {{ chatBusy ? '生成回答中，完成后可切换' : `共 ${sessions.length} 个 · 仅存本浏览器` }}
        </div>
      </aside>

      <section class="panel chat-panel">
        <div class="panel-head">
          <h2 class="chat-title" :title="activeTitle">{{ activeTitle }}</h2>
          <span class="mono panel-note">{{ activeMessages.length }} 条 · {{ chatProvider || config?.label || '等待配置' }}</span>
        </div>

        <div class="context-bar" :class="{ 'is-warn': contextWarn }">
          <div class="context-line">
            <span class="mono context-label">上下文 {{ contextPlan.messages.length }}/{{ AI_CHAT_CONTEXT_LIMIT }} 条</span>
            <span class="context-note">{{ contextNote }}</span>
          </div>
          <div class="context-meter" aria-hidden="true"><span :style="{ width: `${contextPct}%` }"></span></div>
        </div>

        <div class="log-wrap">
          <div ref="chatLog" class="chat-log" aria-live="polite" @scroll="onLogScroll">
            <div
              v-for="message in activeMessages"
              :key="message.id"
              class="chat-row"
              :class="`is-${message.role}`"
            >
              <span class="chat-role mono">{{ message.role === 'user' ? 'YOU' : 'AI' }}</span>
              <div class="chat-content">
                <div class="chat-bubble" :class="{ 'is-collapsed': isLong(message) && !isExpanded(message.id) }">
                  {{ displayContent(message) }}
                </div>
                <button
                  v-if="isLong(message)"
                  class="expand-link mono"
                  type="button"
                  @click="toggleExpanded(message.id)"
                >
                  {{ isExpanded(message.id) ? '收起' : `展开全文 · ${chatCharCount(message.content)} 字` }}
                </button>
              </div>
            </div>
            <div v-if="chatBusy" class="chat-row is-assistant">
              <span class="chat-role mono">AI</span>
              <div class="chat-content">
                <div class="chat-bubble is-typing">正在读取资产库并生成回答…</div>
              </div>
            </div>
          </div>
          <button v-if="!atBottom" class="btn jump-latest" type="button" @click="scrollLatest">
            ↓ 最新<template v-if="unseen"> · {{ unseen }}</template>
          </button>
        </div>

        <div v-if="chatError" class="inline-error chat-error">{{ chatError }}</div>
        <div v-if="sessionFull" class="inline-error chat-error">
          本会话已接近 {{ AI_SESSION_MESSAGE_LIMIT }} 条记录上限，请新建会话继续。
        </div>

        <div class="quick-row">
          <button
            v-for="prompt in quickPrompts"
            :key="prompt"
            class="chip quick-chip"
            type="button"
            :disabled="chatBusy || modelSwitching || sessionFull || !chatReady"
            @click="usePrompt(prompt)"
          >
            {{ prompt }}
          </button>
        </div>

        <div class="composer">
          <textarea
            ref="chatInputEl"
            v-model="chatInput"
            class="field chat-input"
            rows="2"
            placeholder="问资产分类、格式、装配或 sand3d 用法…"
            @keydown="onChatKey"
          />
          <div class="composer-foot">
            <span class="composer-help mono">
              <span :class="{ 'is-over': inputTooLong }">{{ chatCharCount(chatInput) }}/{{ AI_CHAT_MESSAGE_LIMIT }}</span>
              · <RouterLink to="/settings?tab=model" class="config-link">模型配置</RouterLink>
              · {{ chatReady ? 'Enter 发送 · Shift+Enter 换行' : '先保存模型配置' }}
            </span>
            <button class="btn btn-primary" type="button" :disabled="!canSend" @click="sendChat()">发送</button>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>

<style scoped>
.ai-page {
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
  padding-bottom: 6px;
  border-bottom: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
  flex: none;
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

.ai-head-status {
  display: flex;
  flex: none;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
  color: #333333;
  font-size: 9px;
}

.model-switch {
  display: flex;
  align-items: center;
  gap: 2px;
  max-width: 100%;
}

.model-label {
  flex: none;
  color: #333333;
  font-size: 9px;
}

.model-select {
  width: 176px;
  max-width: 100%;
  height: 19px;
  padding: 0 2px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
  color: #111111;
  font: inherit;
  font-size: 9px;
}

.model-select:disabled {
  background: #dcdcdc;
  color: #777777;
}

.model-refresh {
  flex: none;
  width: 19px;
  height: 19px;
  padding: 0;
  border: 1px solid #808080;
  background: #c0c0c0;
  color: #000000;
  font: inherit;
  font-size: 11px;
  line-height: 15px;
}

.model-refresh:hover:not(:disabled) {
  background: #dcdcdc;
}

.model-refresh:disabled {
  color: #888888;
}

.status-row {
  display: flex;
  align-items: center;
  gap: 4px;
}

.model-error {
  max-width: 230px;
  overflow: hidden;
  color: #800000;
  font-size: 8px;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.status-pill {
  padding: 1px 5px;
  border: 1px solid #808080;
  background: #c0c0c0;
  color: #333333;
  font-size: 9px;
  line-height: 1.4;
}

.status-pill.is-ok {
  border-color: #006400;
  color: #006400;
}

.status-pill.is-bad {
  border-color: #800000;
  color: #800000;
}

.inline-error {
  margin: 6px 0;
  padding: 3px 6px;
  border: 1px solid #800000;
  background: #fff0f0;
  color: #800000;
  font-size: 10px;
}

.ai-workspace {
  display: grid;
  grid-template-columns: 196px minmax(0, 1fr);
  gap: 8px;
  flex: 1;
  min-height: 0;
  margin-top: 8px;
}

.panel {
  border: 2px solid;
  border-color: var(--bevel-raised);
  background: var(--surface);
}

.sessions-panel,
.chat-panel {
  display: flex;
  flex-direction: column;
  min-height: 0;
  margin-top: 0;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  flex: none;
  padding: 2px 5px;
  border-bottom: 1px solid #808080;
  background: #c0c0c0;
}

.panel-head h2 {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-display);
  font-size: 11px;
  font-weight: 700;
}

.panel-note {
  flex: none;
  max-width: 46%;
  overflow: hidden;
  color: #333333;
  font-size: 9px;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-new {
  flex: none;
  padding: 0 6px;
  font-size: 9px;
  line-height: 17px;
}

.session-list {
  flex: 1;
  min-height: 80px;
  overflow-y: auto;
  padding: 3px;
  background: #ffffff;
}

.session-item {
  display: flex;
  align-items: stretch;
  border-bottom: 1px dotted #a0a0a0;
  background: #efede6;
}

.session-item:last-child {
  border-bottom: 0;
}

.session-item.is-active {
  background: #000080;
}

.session-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: flex-start;
  gap: 1px;
  min-width: 0;
  padding: 4px 5px;
  border: 0;
  background: transparent;
  color: #111111;
  text-align: left;
}

.session-main:disabled {
  color: #777777;
}

.session-title {
  width: 100%;
  overflow: hidden;
  font-size: 10px;
  font-weight: 700;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-meta {
  color: #555555;
  font-size: 8px;
  line-height: 1.3;
}

.session-item.is-active .session-title {
  color: #ffffff;
}

.session-item.is-active .session-meta {
  color: #ffffcc;
}

.session-item.is-active .session-del {
  border-left-color: #ffffff;
  color: #ffffcc;
}

.session-del {
  flex: none;
  width: 18px;
  padding: 0;
  border: 0;
  border-left: 1px dotted #a0a0a0;
  background: transparent;
  color: #555555;
  font: inherit;
  font-size: 11px;
  line-height: 1;
}

.session-del:hover:not(:disabled) {
  background: #800000;
  color: #ffffff;
}

.session-del:disabled {
  color: #aaaaaa;
}

.session-foot {
  flex: none;
  padding: 3px 5px;
  border-top: 1px solid #808080;
  background: #dcdcdc;
  color: #333333;
  font-size: 8px;
  line-height: 1.35;
}

.chat-title {
  flex: 1;
}

.context-bar {
  flex: none;
  padding: 3px 6px;
  border-bottom: 1px solid #808080;
  background: #dcdcdc;
}

.context-bar.is-warn {
  border-bottom-color: #800000;
  background: #fff0f0;
}

.context-line {
  display: flex;
  align-items: flex-start;
  gap: 6px;
}

.context-label {
  flex: none;
  color: #000080;
  font-size: 9px;
  font-weight: 700;
  line-height: 1.35;
}

.context-bar.is-warn .context-label {
  color: #800000;
}

.context-note {
  min-width: 0;
  color: #333333;
  font-size: 9px;
  line-height: 1.35;
}

.context-meter {
  height: 4px;
  margin-top: 3px;
  border: 1px solid #808080;
  background: #ffffff;
}

.context-meter span {
  display: block;
  height: 100%;
  background: #000080;
  transition: width 120ms linear;
}

.context-bar.is-warn .context-meter span {
  background: #800000;
}

.log-wrap {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
  margin: 6px;
}

.chat-log {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 5px;
  min-height: 0;
  padding: 5px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
  overflow-y: auto;
}

.chat-row {
  display: grid;
  grid-template-columns: 25px minmax(0, 1fr);
  gap: 4px;
  align-items: start;
  flex: none;
}

.chat-row.is-user .chat-role {
  color: #000080;
}

.chat-role {
  padding-top: 3px;
  color: #666666;
  font-size: 8px;
  font-weight: 700;
  text-align: right;
}

.chat-content {
  min-width: 0;
}

.chat-bubble {
  padding: 4px 6px;
  border: 1px solid #808080;
  background: #f4f1ea;
  color: #111111;
  font-size: 10px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.chat-bubble.is-collapsed {
  max-height: 11.2em;
  overflow: hidden;
}

.chat-row.is-user .chat-bubble {
  border-color: #000080;
  background: #000080;
  color: #ffffff;
}

.chat-bubble.is-typing {
  color: #666666;
  font-style: italic;
}

.expand-link {
  display: inline-block;
  margin: 1px 0 0;
  padding: 0 3px;
  border: 1px solid #808080;
  background: #dcdcdc;
  color: #000080;
  font-size: 8px;
  line-height: 16px;
  text-decoration: underline;
}

.expand-link:hover {
  background: #c0c0c0;
}

.jump-latest {
  position: absolute;
  right: 8px;
  bottom: 8px;
  padding: 1px 6px;
  font-size: 9px;
  line-height: 17px;
}

.chat-error {
  margin: 0 6px 4px;
  flex: none;
}

.quick-row {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  gap: 3px;
  padding: 0 6px 4px;
}

.quick-chip {
  max-width: 100%;
  height: auto;
  min-height: 18px;
  padding: 1px 4px;
  white-space: normal;
  text-align: left;
  font-size: 9px;
  line-height: 1.3;
}

.composer {
  flex: none;
  margin: 0 6px 6px;
  padding-top: 4px;
  border-top: 1px solid #808080;
}

.chat-input {
  resize: vertical;
  min-height: 38px;
  max-height: 90px;
  font-size: 10px;
  line-height: 1.45;
}

.composer-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  margin-top: 3px;
}

.composer-help {
  min-width: 0;
  overflow: hidden;
  color: #666666;
  font-size: 8px;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer-help .is-over {
  color: #800000;
  font-weight: 700;
}

.config-link {
  color: #000080;
  text-decoration: underline;
}

@media (max-width: 760px) {
  .ai-workspace {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto minmax(0, 1fr);
    gap: 6px;
  }

  .sessions-panel {
    max-height: 78px;
  }

  .session-list {
    display: flex;
    min-height: 0;
    max-height: 48px;
    overflow-x: auto;
    overflow-y: hidden;
    padding: 2px;
  }

  .session-item {
    flex: none;
    width: 166px;
    border-right: 1px dotted #a0a0a0;
    border-bottom: 0;
  }

  .session-item:last-child {
    border-right: 0;
  }

  .session-foot {
    display: none;
  }

  .chat-panel .panel-note {
    max-width: 55%;
  }
}

@media (max-width: 620px) {
  .status-row {
    display: none;
  }

  .model-label {
    display: none;
  }

  .model-select {
    width: 142px;
  }

  .model-error {
    display: none;
  }

  .context-line {
    display: block;
  }

  .context-note {
    display: block;
    margin-top: 1px;
  }
}
</style>
