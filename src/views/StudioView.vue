<script setup lang="ts">
import { useRouter } from 'vue-router'
import SandLab from '../components/SandLab.vue'

const router = useRouter()
</script>

<template>
  <main class="studio">
    <header class="head">
      <div class="head-main">
        <p class="mono eyebrow">SAND3D / 代码建模</p>
        <h1>从一段 Python 新建模型</h1>
        <p class="lede">
          这段代码交给本机 bpy 真跑，导出的 GLB 先在这里预览，满意了再存成资产。
          不需要依附任何一个已有资产。
        </p>
      </div>
      <div class="head-actions">
        <button class="btn btn-ghost" type="button" @click="router.push('/')">← 返回资产库</button>
      </div>
    </header>

    <div class="lab-host">
      <SandLab standalone />
    </div>
  </main>
</template>

<style scoped>
.studio {
  display: flex;
  flex-direction: column;
  height: 100vh;
  min-height: 0;
  background: var(--surface);
}

.head {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 4px 6px;
  border-bottom: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
}

.head-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.eyebrow {
  color: #000080;
  font-size: 9px;
  letter-spacing: 0;
}

h1 {
  color: #000000;
  font-size: 13px;
}

.lede {
  max-width: 620px;
  color: #333333;
  font-size: 10px;
  line-height: 1.4;
}

.head-actions {
  margin-left: auto;
  display: flex;
  gap: 3px;
}

.lab-host {
  flex: 1;
  min-height: 0;
}
</style>
