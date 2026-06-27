<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Link, Refresh } from '@element-plus/icons-vue'
import { getMarketplaceApp } from '@/api/app'
import type { MarketplaceApp } from '@/types/app'
import { openAppById } from '@/utils/appLaunch'
import { formatDateTime, productStatusLabel } from '@/utils/display'

const route = useRoute()
const router = useRouter()
const appId = computed(() => Number(route.params.id))
const loading = ref(false)
const app = ref<MarketplaceApp | null>(null)

onMounted(fetchDetail)

async function fetchDetail() {
  if (!appId.value) return
  loading.value = true
  try {
    app.value = await getMarketplaceApp(appId.value)
  } finally {
    loading.value = false
  }
}

async function openApp() {
  if (!app.value) return
  await openAppById(app.value.id)
}
</script>

<template>
  <div class="app-detail-page">
    <div class="page-container">
      <div class="page-header">
        <el-button :icon="ArrowLeft" text @click="router.push('/marketplace')">返回商品市场</el-button>
        <el-button :icon="Refresh" :loading="loading" @click="fetchDetail">刷新</el-button>
      </div>

      <section v-loading="loading" class="app-panel glass-card">
        <template v-if="app">
          <div class="app-head">
            <img v-if="app.icon_url" class="app-icon" :src="app.icon_url" :alt="app.name" />
            <div class="app-icon placeholder" v-else>{{ app.name.slice(0, 1) }}</div>
            <div>
              <span class="page-kicker">{{ app.type }}</span>
              <h2>{{ app.name }}</h2>
              <p>{{ app.code }}</p>
            </div>
            <el-tag :type="app.status === 'active' ? 'success' : 'info'">
              {{ productStatusLabel(app.status) }}
            </el-tag>
          </div>

          <p class="app-desc">{{ app.description || '暂无应用说明' }}</p>

          <div class="info-grid">
            <div>
              <span>应用 ID</span>
              <strong>{{ app.id }}</strong>
            </div>
            <div>
              <span>创建时间</span>
              <strong>{{ formatDateTime(app.created_at) }}</strong>
            </div>
          </div>

          <div class="actions">
            <el-button
              v-if="app.access_url"
              type="primary"
              size="large"
              :icon="Link"
              @click="openApp"
            >
              进入应用
            </el-button>
          </div>
        </template>
        <el-empty v-else-if="!loading" description="应用不存在或未上架" />
      </section>
    </div>
  </div>
</template>

<style scoped>
.app-detail-page { padding: 32px 0 0; }
.page-header { display: flex; justify-content: space-between; margin-bottom: 18px; }
.app-panel {
  position: relative;
  overflow: hidden;
  padding: 26px;
  border-radius: 8px;
}
.app-panel::before {
  content: '';
  position: absolute;
  inset: 0 0 auto;
  height: 3px;
  background: var(--gradient-primary);
}
.app-head {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr) auto;
  gap: 16px;
  align-items: center;
}
.app-icon {
  width: 64px;
  height: 64px;
  border-radius: 8px;
  object-fit: cover;
  border: 1px solid var(--color-border);
  background: rgba(255, 255, 255, 0.04);
}
.app-icon.placeholder {
  display: grid;
  place-items: center;
  color: var(--color-accent);
  font-size: 24px;
  font-weight: 800;
}
.page-kicker { color: var(--color-accent); font-size: 12px; font-weight: 700; }
.app-head h2 { margin: 8px 0 6px; color: var(--color-text); }
.app-head p { color: var(--color-text-disabled); font-size: 12px; }
.app-desc { margin-top: 24px; color: var(--color-text-muted); line-height: 1.8; }
.info-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 22px;
}
.info-grid div {
  display: grid;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.03);
}
.info-grid span { color: var(--color-text-muted); font-size: 12px; }
.info-grid strong { color: var(--color-text); font-size: 13px; }
.actions { display: flex; margin-top: 24px; }
@media (max-width: 700px) {
  .app-head { grid-template-columns: 52px minmax(0, 1fr); }
  .app-head .el-tag { grid-column: 1 / -1; width: fit-content; }
  .app-icon { width: 52px; height: 52px; }
  .info-grid { grid-template-columns: 1fr; }
  .actions .el-button { width: 100%; }
}
</style>
