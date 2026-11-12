import { ref } from 'vue'
import { api } from '../lib/api'
import { readError } from '../lib/download'
import type { AIConfig, AIConfigPatch, AITestResult } from '../types'

/** AI 页与详情页共享的运行时配置快照。密钥永远只以尾号形式出现。 */
export const aiConfig = ref<AIConfig | null>(null)
export const aiConfigLoading = ref(false)
export const aiConfigSaving = ref(false)
export const aiConfigTesting = ref(false)
export const aiConfigError = ref('')
export const aiTestResult = ref<AITestResult | null>(null)

let configRequest: Promise<AIConfig> | null = null

export async function loadAIConfig(force = false): Promise<AIConfig | null> {
  if (aiConfig.value && !force) return aiConfig.value
  if (configRequest) return configRequest
  aiConfigLoading.value = true
  aiConfigError.value = ''
  configRequest = api.aiConfig()
  try {
    const config = await configRequest
    aiConfig.value = config
    return config
  } catch (error) {
    aiConfigError.value = readError(error)
    return null
  } finally {
    aiConfigLoading.value = false
    configRequest = null
  }
}

export async function saveAIConfig(patch: AIConfigPatch): Promise<AIConfig | null> {
  if (aiConfigSaving.value) return null
  aiConfigSaving.value = true
  aiConfigError.value = ''
  try {
    const config = await api.saveAIConfig(patch)
    aiConfig.value = config
    return config
  } catch (error) {
    aiConfigError.value = readError(error)
    return null
  } finally {
    aiConfigSaving.value = false
  }
}

export async function testAIConfig(patch: AIConfigPatch): Promise<AITestResult | null> {
  if (aiConfigTesting.value) return null
  aiConfigTesting.value = true
  aiConfigError.value = ''
  aiTestResult.value = null
  try {
    const result = await api.testAIConfig(patch)
    aiTestResult.value = result
    return result
  } catch (error) {
    aiConfigError.value = readError(error)
    return null
  } finally {
    aiConfigTesting.value = false
  }
}

export function clearAITestResult(): void {
  aiTestResult.value = null
}

/** 测试失败时不应把旧的成功结果继续显示成“当前可用”。 */
export function markAIUnavailable(message: string): void {
  aiTestResult.value = { ok: false, message, provider: aiConfig.value?.provider ?? '' }
}
