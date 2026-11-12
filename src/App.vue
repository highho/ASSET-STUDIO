<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppSidebar from './components/AppSidebar.vue'
import OnboardingWizard from './components/OnboardingWizard.vue'
import ToastStack from './components/ToastStack.vue'
import { initLibrary } from './stores/library'

const ONBOARDING_KEY = 'assetstudio.onboarded.v1'
const showOnboarding = ref(false)

onMounted(() => {
  void initLibrary()
  try {
    showOnboarding.value = !localStorage.getItem(ONBOARDING_KEY)
  } catch {
    showOnboarding.value = true
  }
})

function closeOnboarding(): void {
  try {
    localStorage.setItem(ONBOARDING_KEY, '1')
  } catch {
    // 隐私模式写不进去就让引导每次启动出现，不阻塞主流程
  }
  showOnboarding.value = false
}
</script>

<template>
  <div class="shell">
    <AppSidebar />
    <main class="shell-main">
      <RouterView />
    </main>
    <ToastStack />
    <OnboardingWizard v-if="showOnboarding" @close="closeOnboarding" />
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 232px minmax(0, 1fr);
  height: 100%;
}

.shell-main {
  min-width: 0;
  min-height: 0;
  height: 100%;
  overflow: hidden;
}

@media (max-width: 900px) {
  .shell {
    grid-template-columns: 1fr;
    grid-template-rows: auto minmax(0, 1fr);
  }
}
</style>
