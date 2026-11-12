<script setup lang="ts">
import { computed } from 'vue'
import { formatBytes, formatCount, relativeTime } from '../lib/format'
import { getThumbUrl } from '../stores/library'
import type { AssetMeta } from '../types'

const props = defineProps<{
  asset: AssetMeta
  selected: boolean
}>()

const emit = defineEmits<{
  open: []
  toggle: []
  retryThumb: []
}>()

const thumbFailed = computed(() => !props.asset.hasThumb && props.asset.thumbState === 'error')
</script>

<template>
  <article class="asset-card" :class="{ 'is-selected': props.selected }">
    <button class="thumb" type="button" :aria-label="`打开 ${props.asset.name}`" @click="emit('open')">
      <img v-if="getThumbUrl(props.asset.id)" :src="getThumbUrl(props.asset.id)" :alt="props.asset.name" />
      <span v-else-if="thumbFailed" class="thumb-empty mono is-error">PREVIEW FAILED</span>
      <span v-else class="thumb-empty mono">NO PREVIEW</span>
      <span class="format mono">{{ props.asset.format }}</span>
      <span class="open-hint mono">打开检视</span>
    </button>

    <button
      v-if="thumbFailed"
      class="thumb-retry mono"
      type="button"
      title="重新生成缩略图"
      @click.stop="emit('retryThumb')"
    >
      重试
    </button>

    <button
      class="select"
      type="button"
      :aria-pressed="props.selected"
      :aria-label="props.selected ? '取消选择' : '选择此资产'"
      @click.stop="emit('toggle')"
    >
      <span class="select-dot"></span>
    </button>

    <div class="meta">
      <h3 class="name" :title="props.asset.name">{{ props.asset.name }}</h3>
      <div class="metrics mono">
        <span>{{ formatBytes(props.asset.fileSize) }}</span>
        <span class="sep">/</span>
        <span>{{ formatCount(props.asset.stats.triangles) }} tris</span>
      </div>
      <div class="foot">
        <span class="category">{{ props.asset.category || '未分类' }}</span>
        <span class="time mono">{{ relativeTime(props.asset.createdAt) }}</span>
      </div>
    </div>
  </article>
</template>

<style scoped>
.asset-card {
  position: relative;
  display: flex;
  flex-direction: column;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: var(--surface);
  box-shadow: 1px 1px 0 #000000;
  overflow: hidden;
}

.asset-card:hover {
  border-color: #000080;
}

.asset-card.is-selected {
  border-color: #000080;
  box-shadow: inset 0 0 0 1px #000080;
}

.thumb {
  position: relative;
  display: block;
  width: 100%;
  aspect-ratio: 16 / 10;
  padding: 0;
  border: none;
  border-bottom: 2px solid #808080;
  background: #ffffff;
  overflow: hidden;
}

.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.thumb-empty {
  display: grid;
  place-items: center;
  width: 100%;
  height: 100%;
  color: #333333;
  font-size: 10px;
  letter-spacing: 0;
}

.thumb-empty.is-error {
  color: #800000;
}

.thumb-retry {
  position: absolute;
  right: 4px;
  top: 4px;
  z-index: 2;
  padding: 1px 4px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: var(--surface);
  color: #800000;
  font-family: inherit;
  font-size: 9px;
  letter-spacing: 0;
  cursor: pointer;
}

.thumb-retry:hover {
  border-color: var(--bevel-sunken);
  background: #c0c0c0;
}

.format {
  position: absolute;
  left: 4px;
  top: 4px;
  padding: 1px 4px;
  border: 1px solid #808080;
  border-radius: 0;
  background: #c0c0c0;
  color: #000080;
  font-size: 9px;
  letter-spacing: 0;
  text-transform: uppercase;
}

.open-hint {
  position: absolute;
  right: 4px;
  bottom: 4px;
  padding: 1px 3px;
  border: 1px solid #404040;
  border-radius: 0;
  background: #d4d0c8;
  color: #000000;
  font-size: 9px;
  letter-spacing: 0;
  opacity: 0;
}

.asset-card:hover .open-hint {
  opacity: 1;
}

.select {
  position: absolute;
  right: 4px;
  top: 4px;
  width: 16px;
  height: 16px;
  padding: 0;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: #c0c0c0;
  display: grid;
  place-items: center;
}

.select-dot {
  width: 6px;
  height: 6px;
  border-radius: 0;
  background: transparent;
}

.select[aria-pressed="true"] {
  border-color: var(--bevel-sunken);
  background: #ffffff;
}

.select[aria-pressed="true"] .select-dot {
  background: #000080;
}

.meta {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 5px 7px 6px;
}

.name {
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.metrics {
  display: flex;
  gap: 3px;
  color: #333333;
  font-size: 10px;
}

.metrics .sep {
  color: #666666;
}

.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  color: #333333;
  font-size: 9px;
}

.category {
  padding: 0 3px;
  border: 1px solid #808080;
  border-radius: 0;
  background: #c0c0c0;
  letter-spacing: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.time {
  flex: none;
}
</style>
