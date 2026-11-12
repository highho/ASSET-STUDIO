import { createRouter, createWebHistory } from 'vue-router'
import LibraryView from './views/LibraryView.vue'

/**
 * 除资产库外的页面都按需加载：检视台（three + 导出器）、代码模式（bpy 客户端）、
 * 装配台（three 场景）各自只在进入时下载，资产库首屏不再背着它们一起加载。
 */
const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', name: 'library', component: LibraryView },
    {
      path: '/asset/:id',
      name: 'asset',
      component: () => import('./views/AssetDetailView.vue'),
      props: true,
    },
    // 代码建模是独立入口：不该借某个资产页面的「代码」页签才能新建模型。
    { path: '/studio', name: 'studio', component: () => import('./views/StudioView.vue') },
    // 装配台同理：组合多个资产的地方，不从某个资产页的页签进去。
    { path: '/assembly', name: 'assembly', component: () => import('./views/AssemblyView.vue') },
    // 分类工作台是从资产库页头「装配台」后面进入的独立页，不塞回 AI 助手页。
    { path: '/classify', name: 'classify', component: () => import('./views/ClassifyView.vue') },
    // AI 页只保留助手对话；运行时、密钥与常规设置统一进设置页。
    { path: '/ai', name: 'ai', component: () => import('./views/AIView.vue') },
    { path: '/settings', name: 'settings', component: () => import('./views/SettingsView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

export default router
