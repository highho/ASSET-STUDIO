<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { formatBytes } from '../lib/format'
import { MODEL_WITH_SIDECAR_ACCEPT } from '../types'
import { assetCount, backendOnline, importFiles, importProgress, totalBytes } from '../stores/library'

const route = useRoute()
const fileInput = ref<HTMLInputElement | null>(null)

const isLibrary = computed(() => route.name === 'library')
const isAI = computed(() => route.name === 'ai')
const isSettings = computed(() => route.name === 'settings')

function openPicker(): void {
  fileInput.value?.click()
}

function onPick(event: Event): void {
  const input = event.target as HTMLInputElement
  if (input.files) void importFiles([...input.files])
  input.value = ''
}
</script>

<template>
  <aside class="sidebar">
    <div class="brand">
      <svg class="brand-mark" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
        <path d="M12 3.2 20.6 8v8L12 20.8 3.4 16V8z" />
        <path d="M3.4 8 12 13l8.6-5M12 13v7.8" />
      </svg>
      <div class="brand-text">
        <strong>ASSET STUDIO</strong>
        <span>三维资产检视台</span>
      </div>
    </div>

    <nav class="nav">
      <RouterLink to="/" class="nav-item" :class="{ 'is-active': isLibrary }" active-class="is-active">
        <span class="nav-index mono">01</span>
        <span>资产库</span>
        <span class="nav-count mono">{{ assetCount }}</span>
      </RouterLink>
      <RouterLink to="/ai" class="nav-item" :class="{ 'is-active': isAI }" active-class="is-active">
        <span class="nav-index mono">02</span>
        <span>AI 助手</span>
        <span class="nav-hint mono">对话</span>
      </RouterLink>
      <RouterLink to="/settings" class="nav-item" :class="{ 'is-active': isSettings }" active-class="is-active">
        <span class="nav-index mono">03</span>
        <span>设置</span>
        <span class="nav-hint mono">常规 / 模型</span>
      </RouterLink>
    </nav>

    <div class="side-block">
      <button class="btn btn-primary import-btn" type="button" :disabled="!!importProgress" @click="openPicker">
        {{ importProgress ? `导入中 ${importProgress.done}/${importProgress.total}` : '导入模型' }}
      </button>
      <input
        ref="fileInput"
        class="sr-only"
        type="file"
        multiple
        :accept="MODEL_WITH_SIDECAR_ACCEPT"
        @change="onPick"
      />
      <p class="import-note mono">GLB · GLTF · OBJ · STL · FBX（可连同 .bin / 贴图一起选）</p>
    </div>

    <div class="side-block stats">
      <div class="stat-row">
        <span>资产</span>
        <strong class="mono">{{ assetCount }}</strong>
      </div>
      <div class="stat-row">
        <span>总体量</span>
        <strong class="mono">{{ formatBytes(totalBytes) }}</strong>
      </div>
      <div class="storage">
        <div class="storage-head">
          <span>本机 SQLite 资产库</span>
          <span class="mono">{{ formatBytes(totalBytes) }}</span>
        </div>
        <div class="storage-status mono" :class="{ 'is-off': !backendOnline }">
          {{ backendOnline ? 'GO 服务 · 已连接' : 'GO 服务 · 未连接' }}
        </div>
      </div>
    </div>

    <div class="side-foot">
      <p>元数据存 SQLite，模型存 data/models</p>
      <p>换设备时拷贝整个 data 目录</p>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 100%;
  height: 100%;
  padding: 0;
  border-right: 2px solid #808080;
  background: var(--surface);
  box-shadow: inset -1px 0 0 #ffffff;
  overflow-y: auto;
}

.brand {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px;
  border-bottom: 2px solid #404040;
  background: linear-gradient(180deg, #000080 0%, #1084d0 100%);
  color: #ffffff;
}

.brand-mark {
  width: 18px;
  height: 18px;
  color: #ffffff;
  flex: none;
}

.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.2;
}

.brand-text strong {
  font-family: var(--font-display);
  font-size: 11px;
  letter-spacing: 0;
  color: #ffffff;
}

.brand-text span {
  color: #d4d0c8;
  font-size: 9px;
  letter-spacing: 0;
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin: 0 6px;
  padding-top: 0;
  border-top: 0;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 4px;
  border-radius: 0;
  color: #000000;
  font-size: 11px;
}

.nav-item:hover {
  background: #c0c0c0;
  color: #000000;
}

.nav-item.is-active {
  background: #000080;
  color: #ffffff;
  box-shadow: none;
}

.nav-item.is-active .nav-index,
.nav-item.is-active .nav-count,
.nav-item.is-active .nav-hint {
  color: #d4d0c8;
}

.nav-item.is-static {
  cursor: default;
  color: #666666;
}

.nav-index {
  color: #666666;
  font-size: 9px;
  letter-spacing: 0;
}

.nav-count {
  margin-left: auto;
  color: #333333;
  font-size: 10px;
}

.nav-hint {
  margin-left: auto;
  color: #666666;
  font-size: 9px;
}

.side-block {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 0 6px;
}

.import-btn {
  width: 100%;
}

.import-note {
  color: #333333;
  font-size: 9px;
  letter-spacing: 0;
  text-align: center;
}

.stats {
  gap: 2px;
  margin: 0 6px;
  padding: 4px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
}

.stat-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  color: #333333;
  font-size: 10px;
}

.stat-row strong {
  color: #000000;
  font-size: 11px;
  font-weight: 700;
}

.storage {
  margin-top: 3px;
  padding-top: 3px;
  border-top: 1px solid #808080;
}

.storage-head {
  display: flex;
  justify-content: space-between;
  color: #333333;
  font-size: 9px;
  letter-spacing: 0;
}

.storage-status {
  margin-top: 3px;
  padding: 1px 4px;
  border: 1px solid #808080;
  border-radius: 0;
  background: #c0c0c0;
  color: #000080;
  font-size: 9px;
  letter-spacing: 0;
  text-align: center;
}

.storage-status.is-off {
  border-color: #800000;
  color: #800000;
}

.side-foot {
  margin-top: auto;
  padding: 4px 6px 6px;
  color: #333333;
  font-size: 9px;
  line-height: 1.5;
}

@media (max-width: 900px) {
  .sidebar {
    flex-direction: row;
    align-items: center;
    gap: 6px;
    height: auto;
    padding: 3px 4px;
    border-right: none;
    border-bottom: 2px solid #808080;
    overflow: visible;
  }

  .brand {
    flex: none;
    padding: 3px 6px;
    border-bottom: 0;
  }

  .brand-text span,
  .stats,
  .side-foot,
  .import-note,
  .nav-hint,
  .nav-item.is-static {
    display: none;
  }

  .nav {
    flex-direction: row;
    margin: 0;
    padding-top: 0;
    border-top: none;
  }

  .nav-item {
    box-shadow: none;
  }

  .sidebar .side-block {
    margin-left: auto;
    padding: 0;
  }

  .import-btn {
    width: auto;
  }
}
</style>
