<script setup lang="ts">
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
}>()
</script>

<template>
  <div class="row" :class="{ 'is-selected': props.selected }">
    <button
      class="row-select"
      type="button"
      :aria-pressed="props.selected"
      :aria-label="props.selected ? '取消选择' : '选择此资产'"
      @click="emit('toggle')"
    >
      <span class="dot"></span>
    </button>

    <button class="row-main" type="button" @click="emit('open')">
      <span class="row-thumb">
        <img v-if="getThumbUrl(props.asset.id)" :src="getThumbUrl(props.asset.id)" :alt="props.asset.name" />
      </span>
      <span class="row-name">
        <strong>{{ props.asset.name }}</strong>
        <span class="row-tags">{{ props.asset.category || '未分类' }}<span v-if="props.asset.tags.length"> · {{ props.asset.tags.join(' · ') }}</span></span>
      </span>
      <span class="row-format mono">{{ props.asset.format }}</span>
      <span class="row-num mono">{{ formatCount(props.asset.stats.triangles) }}</span>
      <span class="row-num mono">{{ formatBytes(props.asset.fileSize) }}</span>
      <span class="row-time mono">{{ relativeTime(props.asset.createdAt) }}</span>
    </button>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  align-items: center;
  gap: 2px;
  border-bottom: 1px solid #808080;
  background: #ffffff;
}

.row:hover {
  background: #e8e6e1;
}

.row.is-selected {
  background: #000080;
}

.row.is-selected .row-name strong,
.row.is-selected .row-tags,
.row.is-selected .row-format,
.row.is-selected .row-num,
.row.is-selected .row-time {
  color: #ffffff;
}

.row-select {
  display: grid;
  place-items: center;
  width: 24px;
  height: 34px;
  padding: 0;
  border: none;
  background: transparent;
}

.dot {
  width: 9px;
  height: 9px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
}

.row-select[aria-pressed="true"] .dot {
  border-color: var(--bevel-sunken);
  background: #ffffff;
}

.row-select[aria-pressed="true"] .dot::after {
  content: '';
  display: block;
  width: 5px;
  height: 5px;
  background: #000080;
}

.row-main {
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr) 52px 72px 80px 72px;
  align-items: center;
  gap: 8px;
  flex: 1;
  padding: 3px 6px 3px 0;
  border: none;
  background: transparent;
  text-align: left;
}

.row-thumb {
  width: 44px;
  height: 28px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
  overflow: hidden;
}

.row-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.row-name {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.row-name strong {
  font-size: 11.5px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-tags {
  color: #333333;
  font-size: 9.5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-format {
  color: #000080;
  font-size: 9.5px;
  letter-spacing: 0;
  text-transform: uppercase;
}

.row-num,
.row-time {
  color: #333333;
  font-size: 10px;
  text-align: right;
}

.row-time {
  color: #666666;
}

@media (max-width: 1080px) {
  .row-main {
    grid-template-columns: 44px minmax(0, 1fr) 52px 72px;
  }

  .row-main > *:nth-child(4),
  .row-main > *:nth-child(6) {
    display: none;
  }
}
</style>
