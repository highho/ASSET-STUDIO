import type { AIChatMessage } from '../types'

/** 与 server/internal/api/ai.go 的 aiChat 保持一致：只带最近 12 条、单条 4000 字。 */
export const AI_CHAT_CONTEXT_LIMIT = 12
export const AI_CHAT_MESSAGE_LIMIT = 4000
/** 后端 Go 用 rune 计数；这里按 Unicode 码点算，避免中文与 emoji 被算成不同长度。 */
export function chatCharCount(text: string): number {
  return Array.from(text).length
}
/** 浏览器 localStorage 的护栏，避免会话无限堆积。 */
export const AI_SESSION_LIMIT = 30
export const AI_SESSION_MESSAGE_LIMIT = 300
export const AI_CHAT_STORAGE_KEY = 'assetstudio.aiSessions.v1'
export const AI_CHAT_WELCOME =
  '我是 ASSET STUDIO 的本地 AI 助手。可以问我资产分类、格式差异、装配思路、sand3d 代码或这个工具怎么用。'

export interface ChatEntry extends AIChatMessage {
  id: string
  at: number
}

export interface ChatSession {
  id: string
  createdAt: number
  updatedAt: number
  messages: ChatEntry[]
}

export interface ChatPayloadPlan {
  messages: AIChatMessage[]
  /** 因超过 12 条窗口而没有发给模型的更早消息数 */
  dropped: number
  /** 因单条超过 4000 字而被截断的消息数 */
  truncated: number
  /** 本会话可用于对话的消息总数（不含空白消息） */
  total: number
}

function makeId(prefix: string, at: number): string {
  return `${prefix}-${at}-${Math.random().toString(36).slice(2, 8)}`
}

export function newChatEntry(role: ChatEntry['role'], content: string, at = Date.now()): ChatEntry {
  return { id: makeId('msg', at), role, content, at }
}

export function newChatSession(at = Date.now()): ChatSession {
  return {
    id: makeId('chat', at),
    createdAt: at,
    updatedAt: at,
    messages: [newChatEntry('assistant', AI_CHAT_WELCOME, at)],
  }
}

export function chatSessionTitle(session: ChatSession, limit = 20): string {
  const first = session.messages.find((message) => message.role === 'user' && message.content.trim())
  if (!first) return '新会话'
  const flat = first.content.replace(/\s+/g, ' ').trim()
  return flat.length > limit ? `${flat.slice(0, limit)}…` : flat
}

export function chatSessionTime(session: ChatSession): string {
  const date = new Date(session.updatedAt)
  const now = new Date()
  const sameDay =
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
  try {
    return date.toLocaleTimeString('zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
  } catch {
    return sameDay ? '今天' : date.toISOString().slice(5, 10)
  }
}

function readEntry(raw: unknown, index: number, fallbackAt: number): ChatEntry | null {
  if (!raw || typeof raw !== 'object') return null
  const source = raw as Record<string, unknown>
  const role = source.role === 'user' || source.role === 'assistant' ? source.role : null
  if (!role) return null
  const content = typeof source.content === 'string' ? source.content : ''
  if (!content.trim()) return null
  const at = typeof source.at === 'number' && Number.isFinite(source.at) ? source.at : fallbackAt
  return {
    id: typeof source.id === 'string' && source.id ? source.id : `msg-${fallbackAt}-${index}`,
    role,
    content,
    at,
  }
}

function readSession(raw: unknown, index: number): ChatSession | null {
  if (!raw || typeof raw !== 'object') return null
  const source = raw as Record<string, unknown>
  const createdAt = typeof source.createdAt === 'number' && Number.isFinite(source.createdAt) ? source.createdAt : Date.now()
  const updatedAt = typeof source.updatedAt === 'number' && Number.isFinite(source.updatedAt) ? source.updatedAt : createdAt
  if (!Array.isArray(source.messages)) return null
  const messages = source.messages
    .map((entry, entryIndex) => readEntry(entry, entryIndex, updatedAt))
    .filter((entry): entry is ChatEntry => Boolean(entry))
    .slice(-AI_SESSION_MESSAGE_LIMIT)
  if (!messages.length) return null
  return {
    id: typeof source.id === 'string' && source.id ? source.id : `chat-${createdAt}-${index}`,
    createdAt,
    updatedAt,
    messages,
  }
}

/** 读坏数据时直接当作空列表，不让页面因为 localStorage 崩掉。 */
export function loadChatSessions(storage: Storage | null): ChatSession[] {
  if (!storage) return []
  try {
    const raw = storage.getItem(AI_CHAT_STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed
      .map((session, index) => readSession(session, index))
      .filter((session): session is ChatSession => Boolean(session))
      .slice(0, AI_SESSION_LIMIT)
  } catch {
    return []
  }
}

/** 返回 false 表示没写进 localStorage（隐私模式/配额满），页面仍保留内存会话。 */
export function saveChatSessions(storage: Storage | null, sessions: ChatSession[]): boolean {
  if (!storage) return false
  try {
    storage.setItem(AI_CHAT_STORAGE_KEY, JSON.stringify(sessions.slice(0, AI_SESSION_LIMIT)))
    return true
  } catch {
    return false
  }
}

/**
 * 前端先按后端同样的规则裁一遍，页面才能提前告诉用户“哪几条不会发给模型”；
 * 超长消息截断而不是丢掉，避免后端静默跳过造成上下文断裂。
 */
export function planChatPayload(
  messages: ReadonlyArray<AIChatMessage>,
  limit = AI_CHAT_CONTEXT_LIMIT,
  maxChars = AI_CHAT_MESSAGE_LIMIT,
): ChatPayloadPlan {
  const usable = messages.filter((message) => message.content.trim())
  const recent = usable.slice(-limit)
  let truncated = 0
  const payload = recent.map((message) => {
    if (chatCharCount(message.content) <= maxChars) {
      return { role: message.role, content: message.content }
    }
    truncated += 1
    const suffix = '\n…（已截断）'
    const keep = Math.max(1, maxChars - chatCharCount(suffix))
    return {
      role: message.role,
      content: `${Array.from(message.content).slice(0, keep).join('')}${suffix}`,
    }
  })
  return {
    messages: payload,
    dropped: Math.max(0, usable.length - limit),
    truncated,
    total: usable.length,
  }
}
