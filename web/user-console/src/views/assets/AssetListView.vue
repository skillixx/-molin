<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Box, Link, Refresh, Search, Tickets } from '@element-plus/icons-vue'
import { getMyAsset, listMyAssets, listMyEntitlements } from '@/api/asset'
import { getMarketplaceApp } from '@/api/app'
import { getProductDetail } from '@/api/product'
import type { UserAsset, UserEntitlement } from '@/types/asset'
import type { MarketplaceApp } from '@/types/app'
import { formatDateTime } from '@/utils/display'

const loadingAssets = ref(false)
const loadingEntitlements = ref(false)
const detailLoading = ref(false)
const assets = ref<UserAsset[]>([])
const entitlements = ref<UserEntitlement[]>([])
const selectedAsset = ref<UserAsset | null>(null)
const selectedApp = ref<MarketplaceApp | null>(null)
const detailVisible = ref(false)
const launchLoadingAssetId = ref<number | null>(null)

const query = reactive({
  status: '',
  asset_type: '',
  product_id: '',
  page: 1,
  page_size: 20,
})

const filteredAssets = computed(() => {
  return assets.value.filter((item) => {
    const matchType = !query.asset_type || item.asset_type.includes(query.asset_type)
    const matchProduct = !query.product_id || String(item.product_id) === query.product_id
    return matchType && matchProduct
  })
})

const pagedAssets = computed(() => {
  const start = (query.page - 1) * query.page_size
  return filteredAssets.value.slice(start, start + query.page_size)
})

const activeAssets = computed(() => assets.value.filter((item) => item.status === 'active').length)
const activeEntitlements = computed(() => {
  return entitlements.value.filter((item) => item.status === 'active').length
})

onMounted(() => {
  fetchAssets()
  fetchEntitlements()
})

async function fetchAssets() {
  loadingAssets.value = true
  try {
    const res = await listMyAssets({
      status: query.status || undefined,
    })
    assets.value = res.items
    query.page = 1
  } finally {
    loadingAssets.value = false
  }
}

async function fetchEntitlements() {
  loadingEntitlements.value = true
  try {
    const res = await listMyEntitlements()
    entitlements.value = res.items
  } finally {
    loadingEntitlements.value = false
  }
}

function search() {
  query.page = 1
  fetchAssets()
}

function reset() {
  query.status = ''
  query.asset_type = ''
  query.product_id = ''
  query.page = 1
  fetchAssets()
}

function handlePageChange(page: number) {
  query.page = page
}

async function openDetail(row: unknown) {
  const asset = row as UserAsset
  detailVisible.value = true
  detailLoading.value = true
  selectedApp.value = null
  try {
    const detail = await getMyAsset(asset.id)
    selectedAsset.value = detail
    if (isApplicationAsset(detail)) {
      selectedApp.value = await loadApplication(detail)
    }
  } finally {
    detailLoading.value = false
  }
}

function isApplicationAsset(row: Pick<UserAsset, 'asset_type'>) {
  return row.asset_type === 'application'
}

function canLaunchApp(row: unknown) {
  const asset = row as UserAsset
  return isApplicationAsset(asset) && asset.status === 'active'
}

async function loadApplication(row: UserAsset) {
  const { product } = await getProductDetail(row.product_id)
  if (!product.business_ref_id) return null
  return getMarketplaceApp(product.business_ref_id)
}

async function launchApp(row: unknown) {
  const asset = row as UserAsset
  launchLoadingAssetId.value = asset.id
  try {
    const { product } = await getProductDetail(asset.product_id)
    if (!product.business_ref_id) {
      ElMessage.warning('该应用未配置访问地址')
      return
    }
    const app = await getMarketplaceApp(product.business_ref_id)
    if (!app.access_url) {
      ElMessage.warning('该应用暂未开放访问入口')
      return
    }
    window.open(app.access_url, '_blank', 'noopener,noreferrer')
  } finally {
    launchLoadingAssetId.value = null
  }
}

function assetStatusLabel(status: string) {
  const map: Record<string, string> = {
    active: '生效中',
    suspended: '已冻结',
    expired: '已到期',
    cancelled: '已取消',
    pending: '开通中',
  }
  return map[status] ?? status
}

function assetStatusTagType(status: string) {
  if (status === 'active') return 'success'
  if (status === 'pending') return 'warning'
  if (status === 'suspended') return 'danger'
  return 'info'
}

function entitlementStatusLabel(status: string) {
  const map: Record<string, string> = {
    active: '可用',
    suspended: '已冻结',
    expired: '已到期',
  }
  return map[status] ?? status
}

function formatQuota(row: unknown) {
  const item = row as UserEntitlement
  const unit = item.quota_unit ? ` ${item.quota_unit}` : ''
  if (!item.quota_total) return `已用 ${item.quota_used}${unit}`
  return `${item.quota_used} / ${item.quota_total}${unit}`
}
</script>

<template>
  <div class="asset-page">
    <div class="page-container">
      <section class="page-header">
        <div>
          <span class="page-kicker">资产与权益</span>
          <h2 class="page-title">我的资产</h2>
          <p class="page-subtitle">查看购买后开通的资产、业务实例和权益额度。</p>
        </div>
        <el-button :icon="Refresh" :loading="loadingAssets || loadingEntitlements" @click="fetchAssets(); fetchEntitlements()">刷新</el-button>
      </section>

      <section class="summary-grid">
        <div class="summary-card glass-card">
          <el-icon><Box /></el-icon>
          <span>资产总数</span>
          <strong>{{ assets.length }}</strong>
        </div>
        <div class="summary-card glass-card">
          <el-icon><Tickets /></el-icon>
          <span>生效资产</span>
          <strong>{{ activeAssets }}</strong>
        </div>
        <div class="summary-card glass-card">
          <el-icon><Tickets /></el-icon>
          <span>可用权益</span>
          <strong>{{ activeEntitlements }}</strong>
        </div>
      </section>

      <el-tabs class="asset-tabs">
        <el-tab-pane label="资产列表">
          <div class="filter-bar glass-card">
            <el-select v-model="query.status" clearable placeholder="资产状态">
              <el-option label="生效中" value="active" />
              <el-option label="开通中" value="pending" />
              <el-option label="已冻结" value="suspended" />
              <el-option label="已到期" value="expired" />
              <el-option label="已取消" value="cancelled" />
            </el-select>
            <el-input
              v-model="query.asset_type"
              clearable
              placeholder="资产类型"
              :prefix-icon="Search"
              @keyup.enter="search"
            />
            <el-input
              v-model="query.product_id"
              clearable
              placeholder="商品 ID"
              @keyup.enter="search"
            />
            <el-button type="primary" :loading="loadingAssets" @click="search">查询</el-button>
            <el-button @click="reset">重置</el-button>
          </div>

          <el-table
            v-if="loadingAssets || filteredAssets.length > 0"
            v-loading="loadingAssets"
            :data="pagedAssets"
            class="asset-table"
            border
          >
            <el-table-column prop="id" label="资产 ID" width="100" />
            <el-table-column label="资产类型" min-width="130">
              <template #default="{ row }">{{ row.asset_type || '--' }}</template>
            </el-table-column>
            <el-table-column prop="product_id" label="商品 ID" width="110" />
            <el-table-column label="套餐 ID" width="110">
              <template #default="{ row }">{{ row.product_plan_id ?? '--' }}</template>
            </el-table-column>
            <el-table-column label="状态" width="110">
              <template #default="{ row }">
                <el-tag :type="assetStatusTagType(row.status)">
                  {{ assetStatusLabel(row.status) }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="实例 ID" min-width="170">
              <template #default="{ row }">{{ row.business_instance_id || '--' }}</template>
            </el-table-column>
            <el-table-column label="到期时间" min-width="170">
              <template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template>
            </el-table-column>
            <el-table-column label="创建时间" min-width="170">
              <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="190" fixed="right">
              <template #default="{ row }">
                <el-button type="primary" text @click="openDetail(row)">详情</el-button>
                <el-button
                  v-if="canLaunchApp(row)"
                  type="success"
                  text
                  :icon="Link"
                  :loading="launchLoadingAssetId === row.id"
                  @click="launchApp(row)"
                >
                  进入应用
                </el-button>
              </template>
            </el-table-column>
          </el-table>

          <div v-else class="empty-state">
            <div class="empty-icon"><el-icon><Box /></el-icon></div>
            <strong>暂无资产</strong>
            <span>完成商品购买并开通后，资产会显示在这里。</span>
          </div>

          <div v-if="filteredAssets.length > 0" class="pagination-row">
            <el-pagination
              background
              layout="prev, pager, next, total"
              :current-page="query.page"
              :page-size="query.page_size"
              :total="filteredAssets.length"
              @current-change="handlePageChange"
            />
          </div>
        </el-tab-pane>

        <el-tab-pane label="权益额度">
          <el-table
            v-if="loadingEntitlements || entitlements.length > 0"
            v-loading="loadingEntitlements"
            :data="entitlements"
            class="asset-table"
            border
          >
            <el-table-column prop="id" label="权益 ID" width="100" />
            <el-table-column prop="asset_id" label="资产 ID" width="110" />
            <el-table-column label="权益类型" min-width="150">
              <template #default="{ row }">{{ row.entitlement_type }}</template>
            </el-table-column>
            <el-table-column prop="product_id" label="商品 ID" width="110" />
            <el-table-column label="额度使用" min-width="180">
              <template #default="{ row }">{{ formatQuota(row) }}</template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }">
                <el-tag :type="row.status === 'active' ? 'success' : 'info'">
                  {{ entitlementStatusLabel(row.status) }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="到期时间" min-width="170">
              <template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template>
            </el-table-column>
          </el-table>

          <div v-else class="empty-state">
            <div class="empty-icon"><el-icon><Tickets /></el-icon></div>
            <strong>暂无权益额度</strong>
            <span>带额度的资产开通后，权益额度会显示在这里。</span>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>

    <el-drawer v-model="detailVisible" title="资产详情" size="460px" class="asset-detail-drawer">
      <div v-loading="detailLoading" class="asset-detail">
        <template v-if="selectedAsset">
          <div class="detail-status">
            <span>资产 #{{ selectedAsset.id }}</span>
            <el-tag :type="assetStatusTagType(selectedAsset.status)">
              {{ assetStatusLabel(selectedAsset.status) }}
            </el-tag>
          </div>
          <dl>
            <div><dt>资产类型</dt><dd>{{ selectedAsset.asset_type || '--' }}</dd></div>
            <div><dt>商品 ID</dt><dd>{{ selectedAsset.product_id }}</dd></div>
            <div><dt>套餐 ID</dt><dd>{{ selectedAsset.product_plan_id ?? '--' }}</dd></div>
            <div><dt>来源订单 ID</dt><dd>{{ selectedAsset.source_order_id ?? '--' }}</dd></div>
            <div><dt>业务实例 ID</dt><dd>{{ selectedAsset.business_instance_id || '--' }}</dd></div>
            <div><dt>开始时间</dt><dd>{{ formatDateTime(selectedAsset.started_at) }}</dd></div>
            <div><dt>到期时间</dt><dd>{{ formatDateTime(selectedAsset.expires_at) }}</dd></div>
            <div><dt>创建时间</dt><dd>{{ formatDateTime(selectedAsset.created_at) }}</dd></div>
          </dl>

          <section v-if="isApplicationAsset(selectedAsset)" class="app-access-card">
            <div v-if="selectedApp" class="app-access-main">
              <img v-if="selectedApp.icon_url" class="app-access-icon" :src="selectedApp.icon_url" :alt="selectedApp.name" />
              <div v-else class="app-access-icon placeholder">{{ selectedApp.name.slice(0, 1) }}</div>
              <div>
                <span>关联应用</span>
                <strong>{{ selectedApp.name }}</strong>
                <small>{{ selectedApp.code }}</small>
              </div>
            </div>
            <div v-else class="app-access-empty">
              <span>关联应用</span>
              <strong>暂无应用信息</strong>
            </div>
            <el-button
              type="primary"
              :icon="Link"
              :loading="launchLoadingAssetId === selectedAsset.id"
              :disabled="selectedAsset.status !== 'active'"
              @click="launchApp(selectedAsset)"
            >
              进入应用
            </el-button>
          </section>
        </template>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.asset-page {
  padding: 34px 0 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-end;
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 42%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.56);
  box-shadow: var(--shadow-card);
}

.page-kicker {
  display: inline-flex;
  margin-bottom: 10px;
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 700;
}

.page-title {
  margin-bottom: 8px;
}

.summary-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 18px;
}

.summary-card {
  display: grid;
  gap: 8px;
  padding: 20px;
  border-radius: 8px;
}

.summary-card .el-icon {
  color: var(--color-accent);
  font-size: 24px;
}

.summary-card span {
  color: var(--color-text-muted);
  font-size: 13px;
}

.summary-card strong {
  color: var(--color-text);
  font-size: 26px;
}

.asset-tabs {
  padding: 18px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(7, 11, 18, 0.42);
}

.filter-bar {
  display: grid;
  grid-template-columns: 150px minmax(160px, 1fr) 140px auto auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 16px;
  border-radius: 8px;
}

.asset-table {
  width: 100%;
}

.empty-state {
  min-height: 240px;
  display: grid;
  place-items: center;
  align-content: center;
  gap: 10px;
  padding: 36px 20px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.08), transparent 42%),
    rgba(7, 11, 18, 0.58);
  color: var(--color-text-muted);
  text-align: center;
}

.empty-icon {
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  color: var(--color-primary);
  background: rgba(34, 211, 238, 0.1);
}

.empty-icon .el-icon {
  font-size: 24px;
}

.empty-state strong {
  color: var(--color-text);
  font-size: 16px;
}

.empty-state span {
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}

.asset-detail {
  min-height: 220px;
}

:global(.asset-detail-drawer) {
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.98) 0%, rgba(7, 11, 18, 0.98) 100%) !important;
  color: var(--color-text) !important;
}

:global(.asset-detail-drawer .el-drawer__header) {
  margin-bottom: 0;
  padding: 18px 20px;
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text) !important;
}

:global(.asset-detail-drawer .el-drawer__title) {
  color: var(--color-text) !important;
  font-weight: 700;
}

:global(.asset-detail-drawer .el-drawer__close-btn) {
  color: var(--color-text-muted) !important;
}

:global(.asset-detail-drawer .el-drawer__body) {
  padding: 20px;
  background: transparent !important;
  color: var(--color-text) !important;
}

.detail-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 18px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text);
  font-weight: 700;
}

.asset-detail dl {
  display: grid;
  gap: 12px;
}

.asset-detail dl div {
  display: grid;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.03);
}

.asset-detail dt {
  color: var(--color-text-muted);
  font-size: 12px;
}

.asset-detail dd {
  color: var(--color-text);
  font-size: 14px;
  word-break: break-all;
}

.app-access-card {
  display: grid;
  gap: 14px;
  margin-top: 18px;
  padding: 14px;
  border: 1px solid rgba(34, 211, 238, 0.22);
  border-radius: 8px;
  background: rgba(34, 211, 238, 0.08);
}

.app-access-main {
  display: grid;
  grid-template-columns: 48px minmax(0, 1fr);
  gap: 12px;
  align-items: center;
}

.app-access-icon {
  width: 48px;
  height: 48px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.05);
  object-fit: cover;
}

.app-access-icon.placeholder {
  display: grid;
  place-items: center;
  color: var(--color-accent);
  font-size: 20px;
  font-weight: 800;
}

.app-access-main span,
.app-access-empty span {
  display: block;
  color: var(--color-text-muted);
  font-size: 12px;
}

.app-access-main strong,
.app-access-empty strong {
  display: block;
  margin-top: 4px;
  overflow: hidden;
  color: var(--color-text);
  font-size: 15px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-access-main small {
  display: block;
  margin-top: 4px;
  color: var(--color-text-disabled);
  font-size: 12px;
}

.app-access-card .el-button {
  justify-self: flex-start;
}

@media (max-width: 900px) {
  .page-header,
  .filter-bar {
    grid-template-columns: 1fr;
    flex-direction: column;
    align-items: stretch;
  }

  .summary-grid {
    grid-template-columns: 1fr;
  }
}
</style>
