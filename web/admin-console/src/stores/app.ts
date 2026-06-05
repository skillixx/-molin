// 应用全局状态：侧边栏折叠状态 / 页面标题
import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useAppStore = defineStore('app', () => {
  /** 侧边栏是否折叠 */
  const sideMenuCollapsed = ref(false)

  /** 当前页面标题 */
  const pageTitle = ref('仪表盘')

  function toggleSideMenu() {
    sideMenuCollapsed.value = !sideMenuCollapsed.value
  }

  function setPageTitle(title: string) {
    pageTitle.value = title
    document.title = `${title} — 墨灵管理后台`
  }

  return {
    sideMenuCollapsed,
    pageTitle,
    toggleSideMenu,
    setPageTitle,
  }
})
