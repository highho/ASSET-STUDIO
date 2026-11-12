import type { AIProvider } from '../types'

/**
 * Provider 只是“哪家的接口”；协议只有两种：Ollama 原生，或 OpenAI 兼容。
 * 这里只放编辑框的推荐值，不在页面加载时自动写入，避免没配置却悄悄连上某个默认模型。
 */
export interface AIProviderPreset {
  id: AIProvider
  label: string
  endpoint: string
  model: string
  needsKey: boolean
  hint: string
}

export const AI_PROVIDER_PRESETS: AIProviderPreset[] = [
  {
    id: 'ollama',
    label: 'Ollama（本机）',
    endpoint: 'http://127.0.0.1:11434',
    model: 'qwen2.5:7b-instruct',
    needsKey: false,
    hint: '本机 Ollama，不需要密钥；模型名要和 ollama list 里的一致。',
  },
  {
    id: 'openai',
    label: 'OpenAI 官方',
    endpoint: 'https://api.openai.com/v1',
    model: 'gpt-4o-mini',
    needsKey: true,
    hint: 'OpenAI 官方接口，需要 API Key。',
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    endpoint: 'https://api.deepseek.com/v1',
    model: 'deepseek-chat',
    needsKey: true,
    hint: 'DeepSeek 的 OpenAI 兼容接口。',
  },
  {
    id: 'moonshot',
    label: 'Kimi（月之暗面）',
    endpoint: 'https://api.moonshot.cn/v1',
    model: 'moonshot-v1-8k',
    needsKey: true,
    hint: 'Kimi 的 OpenAI 兼容接口。',
  },
  {
    id: 'zhipu',
    label: '智谱 GLM',
    endpoint: 'https://open.bigmodel.cn/api/paas/v4',
    model: 'glm-4-flash',
    needsKey: true,
    hint: '智谱 GLM-4 的 OpenAI 兼容接口。',
  },
  {
    id: 'dashscope',
    label: '通义千问（百炼）',
    endpoint: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    model: 'qwen-plus',
    needsKey: true,
    hint: '阿里云百炼的 OpenAI 兼容接口，适合通义千问系列。',
  },
  {
    id: 'siliconflow',
    label: 'SiliconFlow',
    endpoint: 'https://api.siliconflow.cn/v1',
    model: 'Qwen/Qwen2.5-7B-Instruct',
    needsKey: true,
    hint: 'SiliconFlow 的 OpenAI 兼容接口，模型名区分大小写。',
  },
  {
    id: 'groq',
    label: 'Groq',
    endpoint: 'https://api.groq.com/openai/v1',
    model: 'llama-3.1-8b-instant',
    needsKey: true,
    hint: 'Groq 的低延迟 OpenAI 兼容接口。',
  },
  {
    id: 'lmstudio',
    label: 'LM Studio（本机）',
    endpoint: 'http://127.0.0.1:1234/v1',
    model: 'local-model',
    needsKey: false,
    hint: 'LM Studio 本地 OpenAI 兼容服务，通常不需要密钥。',
  },
  {
    id: 'custom',
    label: '自定义 OpenAI 兼容',
    endpoint: '',
    model: '',
    needsKey: false,
    hint: '任何兼容 /v1/chat/completions 的地址；密钥按服务要求填写。',
  },
]

export function aiProviderPreset(provider: string | undefined): AIProviderPreset | undefined {
  return AI_PROVIDER_PRESETS.find((preset) => preset.id === provider)
}

export function aiProviderLabel(provider: string | undefined): string {
  return aiProviderPreset(provider)?.label ?? '未配置'
}

export function aiProviderNeedsKey(provider: string | undefined): boolean {
  return aiProviderPreset(provider)?.needsKey ?? false
}
