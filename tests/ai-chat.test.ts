import assert from 'node:assert/strict'
import test from 'node:test'

class MemoryStorage {
  private map = new Map<string, string>()
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key) ?? null : null
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value)
  }
  removeItem(key: string): void {
    this.map.delete(key)
  }
  clear(): void {
    this.map.clear()
  }
}

const {
  AI_CHAT_CONTEXT_LIMIT,
  AI_CHAT_MESSAGE_LIMIT,
  AI_CHAT_STORAGE_KEY,
  AI_SESSION_MESSAGE_LIMIT,
  chatSessionTitle,
  loadChatSessions,
  newChatSession,
  planChatPayload,
  saveChatSessions,
} = await import('../src/lib/aiChat.ts')

test('新会话自带欢迎语，没有用户消息时标题是「新会话」', () => {
  const session = newChatSession(1000)
  assert.equal(session.messages.length, 1)
  assert.equal(session.messages[0].role, 'assistant')
  assert.equal(chatSessionTitle(session), '新会话')
})

test('会话标题取第一条用户消息，并压平换行、截断过长标题', () => {
  const session = newChatSession(1000)
  session.messages.push({
    id: 'u1',
    role: 'user',
    content: '  帮我把资产库按\n角色和机械重新整理一下，顺便列出缺标签的  ',
    at: 1001,
  })
  const title = chatSessionTitle(session)
  assert.equal(title.startsWith('帮我把资产库按 角色和机械'), true)
  assert.equal(title.endsWith('…'), true)
})

test('保存后读回会话；坏 JSON 与空消息不会被当成有效会话', () => {
  const storage = new MemoryStorage()
  const session = newChatSession(2000)
  assert.equal(saveChatSessions(storage, [session]), true)
  const loaded = loadChatSessions(storage)
  assert.equal(loaded.length, 1)
  assert.equal(loaded[0].id, session.id)
  assert.equal(loaded[0].messages[0].content.startsWith('我是 ASSET STUDIO'), true)

  storage.setItem(AI_CHAT_STORAGE_KEY, 'not-json')
  assert.deepEqual(loadChatSessions(storage), [])

  storage.setItem(
    AI_CHAT_STORAGE_KEY,
    JSON.stringify([{ id: 'bad', messages: [{ role: 'user', content: '   ' }] }]),
  )
  assert.deepEqual(loadChatSessions(storage), [])
})

test('读回时单会话最多保留 300 条，非法角色被过滤', () => {
  const storage = new MemoryStorage()
  const messages = Array.from({ length: AI_SESSION_MESSAGE_LIMIT + 5 }, (_, index) => ({
    id: `m${index}`,
    role: index % 2 === 0 ? 'user' : 'assistant',
    content: `消息 ${index}`,
    at: index,
  }))
  messages.push({ id: 'bad', role: 'system', content: '不应进来', at: 999 })
  storage.setItem(
    AI_CHAT_STORAGE_KEY,
    JSON.stringify([{ id: 'long', createdAt: 1, updatedAt: 2, messages }]),
  )
  const loaded = loadChatSessions(storage)
  assert.equal(loaded[0].messages.length, AI_SESSION_MESSAGE_LIMIT)
  assert.equal(loaded[0].messages.some((message) => message.role === 'system'), false)
})

test('上下文只取最近 12 条，并报告被丢掉的数量', () => {
  const messages = Array.from({ length: 18 }, (_, index) => ({
    role: index % 2 === 0 ? ('user' as const) : ('assistant' as const),
    content: `第 ${index} 条`,
  }))
  const plan = planChatPayload(messages)
  assert.equal(plan.messages.length, AI_CHAT_CONTEXT_LIMIT)
  assert.equal(plan.messages[0].content, '第 6 条')
  assert.equal(plan.messages.at(-1)?.content, '第 17 条')
  assert.equal(plan.dropped, 6)
  assert.equal(plan.total, 18)
})

test('单条超过 4000 字时截断后仍然发送，不静默丢弃', () => {
  const long = '字'.repeat(AI_CHAT_MESSAGE_LIMIT + 100)
  const plan = planChatPayload([
    { role: 'assistant', content: long },
    { role: 'user', content: '继续' },
  ])
  assert.equal(plan.messages.length, 2)
  assert.equal(plan.truncated, 1)
  assert.equal(plan.messages[0].content.length <= AI_CHAT_MESSAGE_LIMIT, true)
  assert.equal(plan.messages[0].content.endsWith('（已截断）'), true)
  assert.equal(plan.messages[1].content, '继续')
})

test('按 Unicode 码点计算长度，emoji 不会被算成两个字符', () => {
  const atLimit = '😀'.repeat(AI_CHAT_MESSAGE_LIMIT)
  assert.equal(planChatPayload([{ role: 'user', content: atLimit }]).truncated, 0)

  const overLimit = '😀'.repeat(AI_CHAT_MESSAGE_LIMIT + 1)
  const plan = planChatPayload([{ role: 'user', content: overLimit }])
  assert.equal(plan.truncated, 1)
  assert.equal(Array.from(plan.messages[0].content).length <= AI_CHAT_MESSAGE_LIMIT, true)
})

