<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Search } from '@element-plus/icons-vue'
import { listAdminAssets, listUserAssets, operateAdminAsset } from '@/api/asset-admin'
import type { AdminAsset, AdminAssetAction } from '@/types/asset-admin'
import type { Pagination } from '@/types/api'
import { assetStatusLabel, formatDateTime } from '@/utils/display'

const loading = ref(false)
const assets = ref<AdminAsset[]>([])
const searchForm = reactive({ user_id: undefined as number | undefined, status: '' })
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const userAssetsVisible = ref(false)
const userAssetsLoading = ref(false)
const userAssetId = ref<number>()
const userAssets = ref<AdminAsset[]>([])

const operateVisible = ref(false)
const operating = ref(false)
const selectedAsset = ref<AdminAsset | null>(null)
const operateForm = reactive({
  action: 'freeze' as AdminAssetAction,
  remark: '',
})

onMounted(fetchAssets)

async function fetchAssets() {
  loading.value = true
  try {
    // 后端资产列表使用扁平分页，筛选条件为空时不传，避免把空字符串当成有效状态。
    const res = await listAdminAssets({
      user_id: searchForm.user_id,
      status: searchForm.status || undefined,
      page: pagination.page,
      page_size: pagination.page_size,
    })
    assets.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchAssets()
}

function handleReset() {
  searchForm.user_id = undefined
  searchForm.status = ''
  handleSearch()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchAssets()
}

async function openUserAssets() {
  if (!userAssetId.value) {
    ElMessage.warning('请先输入用户 ID')
    return
  }
  // 指定用户资产接口与全量列表不同，不返回分页字段，单独放入抽屉展示。
  userAssetsVisible.value = true
  userAssetsLoading.value = true
  try {
    const res = await listUserAssets(userAssetId.value)
    userAssets.value = res.items
  } finally {
    userAssetsLoading.value = false
  }
}

function openOperate(asset: AdminAsset, action: AdminAssetAction) {
  // 操作弹窗每次打开都重置备注，防止上一次冻结/取消原因被误提交。
  selectedAsset.value = asset
  operateForm.action = action
  operateForm.remark = ''
  operateVisible.value = true
}

async function submitOperate() {
  if (!selectedAsset.value) return
  if (!operateForm.remark.trim()) {
    ElMessage.warning('请填写操作备注')
    return
  }
  operating.value = true
  try {
    // 资产冻结、解冻、取消共用同一个后端端点，取消原因也必须用 remark 字段。
    await operateAdminAsset(selectedAsset.value.id, {
      action: operateForm.action,
      remark: operateForm.remark.trim(),
    })
    ElMessage.success('资产状态已更新')
    operateVisible.value = false
    await fetchAssets()
    if (userAssetsVisible.value && userAssetId.value) {
      // 如果指定用户资产抽屉仍打开，同步刷新抽屉数据，保证两处状态一致。
      const res = await listUserAssets(userAssetId.value)
      userAssets.value = res.items
    }
  } finally {
    operating.value = false
  }
}

function actionLabel(action: AdminAssetAction) {
  const map: Record<AdminAssetAction, string> = { freeze: '冻结', unfreeze: '解冻', cancel: '取消' }
  return map[action]
}

function statusTagType(status: string) {
  if (status === 'active') return 'success'
  if (status === 'suspended') return 'warning'
  if (status === 'cancelled') return 'danger'
  return 'info'
}
</script>

<template>
  <div class="admin-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">用户资产</h3>
        <p class="page-subtitle">查询用户资产并执行冻结、解冻、取消等管理操作</p>
      </div>
      <div class="header-actions">
        <el-input-number v-model="userAssetId" :min="1" :precision="0" controls-position="right" placeholder="用户 ID" />
        <el-button :icon="Search" @click="openUserAssets">查看用户资产</el-button>
      </div>
    </div>

    <div class="filter-card">
      <el-input-number v-model="searchForm.user_id" :min="1" :precision="0" controls-position="right" placeholder="用户 ID" />
      <el-select v-model="searchForm.status" clearable placeholder="资产状态">
        <el-option label="生效中" value="active" />
        <el-option label="已冻结" value="suspended" />
        <el-option label="已过期" value="expired" />
        <el-option label="已取消" value="cancelled" />
      </el-select>
      <el-button type="primary" :icon="Search" :loading="loading" @click="handleSearch">查询</el-button>
      <el-button @click="handleReset">重置</el-button>
      <el-button :icon="Refresh" @click="fetchAssets">刷新</el-button>
    </div>

    <el-table :data="assets" v-loading="loading" border>
      <el-table-column prop="id" label="资产 ID" width="90" />
      <el-table-column prop="user_id" label="用户 ID" width="100" />
      <el-table-column prop="asset_type" label="资产类型" min-width="120" />
      <el-table-column prop="product_id" label="商品 ID" width="100" />
      <el-table-column prop="product_plan_id" label="套餐 ID" width="100" />
      <el-table-column prop="source_order_id" label="来源订单" width="120" />
      <el-table-column prop="business_instance_id" label="业务实例" min-width="150" show-overflow-tooltip />
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="statusTagType(row.status)">{{ assetStatusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="开始时间" min-width="170">
        <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
      </el-table-column>
      <el-table-column label="到期时间" min-width="170">
        <template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template>
      </el-table-column>
      <el-table-column label="创建时间" min-width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="230" fixed="right">
        <template #default="{ row }">
          <el-button v-if="row.status === 'active'" type="warning" text @click="openOperate(row, 'freeze')">冻结</el-button>
          <el-button v-if="row.status === 'suspended'" type="success" text @click="openOperate(row, 'unfreeze')">解冻</el-button>
          <el-button v-if="['active', 'suspended'].includes(row.status)" type="danger" text @click="openOperate(row, 'cancel')">取消</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-row">
      <el-pagination
        background
        layout="prev, pager, next, total"
        :current-page="pagination.page"
        :page-size="pagination.page_size"
        :total="pagination.total"
        @current-change="handlePageChange"
      />
    </div>

    <el-dialog v-model="operateVisible" :title="`${actionLabel(operateForm.action)}资产`" width="520px">
      <el-form label-width="90px">
        <el-form-item label="资产 ID">{{ selectedAsset?.id }}</el-form-item>
        <el-form-item label="操作备注">
          <el-input
            v-model="operateForm.remark"
            type="textarea"
            :rows="4"
            maxlength="200"
            show-word-limit
            placeholder="请填写本次操作原因，后端使用 remark 字段记录"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="operateVisible = false">取消</el-button>
        <el-button type="primary" :loading="operating" @click="submitOperate">确认提交</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="userAssetsVisible" title="指定用户资产" size="72%">
      <el-table :data="userAssets" v-loading="userAssetsLoading" border>
        <el-table-column prop="id" label="资产 ID" width="90" />
        <el-table-column prop="asset_type" label="资产类型" min-width="130" />
        <el-table-column prop="product_id" label="商品 ID" width="100" />
        <el-table-column prop="source_order_id" label="来源订单" width="120" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)">{{ assetStatusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="到期时间" min-width="170">
          <template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template>
        </el-table-column>
      </el-table>
    </el-drawer>
  </div>
</template>

<style scoped>
.admin-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header,
.filter-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px;
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: 8px;
}

.page-title-text {
  margin: 0 0 6px;
  font-size: 18px;
}

.page-subtitle {
  margin: 0;
  color: var(--mc-text-muted);
}

.filter-card,
.header-actions {
  justify-content: flex-start;
  flex-wrap: wrap;
}

.pagination-row {
  display: flex;
  justify-content: flex-end;
}
</style>
