<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { getAdminOrder, listAdminOrders } from '@/api/order-admin'
import type { Order } from '@/types/order-admin'
import type { Pagination } from '@/types/api'
import { displayAmount, formatDateTime, orderStatusLabel, orderTypeLabel } from '@/utils/display'

const loading = ref(false)
const detailLoading = ref(false)
const orders = ref<Order[]>([])
const selectedOrder = ref<Order | null>(null)
const detailVisible = ref(false)
const searchForm = reactive({
  user_id: undefined as number | undefined,
  status: '',
  order_type: '',
  dates: [] as string[],
})
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

onMounted(fetchOrders)

async function fetchOrders() {
  loading.value = true
  try {
    const res = await listAdminOrders({
      user_id: searchForm.user_id,
      status: searchForm.status || undefined,
      order_type: searchForm.order_type || undefined,
      created_from: searchForm.dates[0],
      created_to: searchForm.dates[1],
      page: pagination.page,
      page_size: pagination.page_size,
    })
    orders.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchOrders()
}

function handleReset() {
  searchForm.user_id = undefined
  searchForm.status = ''
  searchForm.order_type = ''
  searchForm.dates = []
  handleSearch()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchOrders()
}

async function openDetail(order: Order) {
  detailVisible.value = true
  detailLoading.value = true
  try {
    selectedOrder.value = await getAdminOrder(order.id)
  } finally {
    detailLoading.value = false
  }
}

function statusTagType(status: string) {
  if (status === 'paid') return 'success'
  if (status === 'pending') return 'warning'
  if (status === 'cancelled') return 'info'
  return 'danger'
}
</script>

<template>
  <div class="order-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">订单管理</h3>
        <p class="page-subtitle">查看全量商品订单和充值订单，支持按用户、类型和状态筛选</p>
      </div>
    </div>

    <div class="filter-card">
      <el-input-number v-model="searchForm.user_id" :min="1" placeholder="用户 ID" />
      <el-select v-model="searchForm.status" clearable placeholder="订单状态">
        <el-option label="待支付" value="pending" />
        <el-option label="已支付" value="paid" />
        <el-option label="已取消" value="cancelled" />
        <el-option label="支付失败" value="failed" />
        <el-option label="已退款" value="refunded" />
      </el-select>
      <el-select v-model="searchForm.order_type" clearable placeholder="订单类型">
        <el-option label="商品订单" value="product" />
        <el-option label="充值订单" value="recharge" />
      </el-select>
      <el-date-picker
        v-model="searchForm.dates"
        type="daterange"
        value-format="YYYY-MM-DD"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
      />
      <el-button type="primary" :loading="loading" @click="handleSearch">查询</el-button>
      <el-button @click="handleReset">重置</el-button>
    </div>

    <el-table :data="orders" v-loading="loading" border>
      <el-table-column prop="order_no" label="订单号" min-width="190" />
      <el-table-column prop="user_id" label="用户 ID" width="100" />
      <el-table-column label="类型" width="120"><template #default="{ row }">{{ orderTypeLabel(row.order_type) }}</template></el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-tag :type="statusTagType(row.status)">{{ orderStatusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="金额" min-width="150">
        <template #default="{ row }">{{ displayAmount(row.amount, row.currency) }}</template>
      </el-table-column>
      <el-table-column label="创建时间" min-width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="100" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" text @click="openDetail(row)">详情</el-button>
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

    <el-drawer v-model="detailVisible" size="60%" title="订单详情">
      <div v-loading="detailLoading">
        <template v-if="selectedOrder">
          <div class="detail-grid">
            <div><span>订单号</span><strong>{{ selectedOrder.order_no }}</strong></div>
            <div><span>用户 ID</span><strong>{{ selectedOrder.user_id }}</strong></div>
            <div><span>订单类型</span><strong>{{ orderTypeLabel(selectedOrder.order_type) }}</strong></div>
            <div><span>订单状态</span><strong>{{ orderStatusLabel(selectedOrder.status) }}</strong></div>
            <div><span>金额</span><strong>{{ displayAmount(selectedOrder.amount, selectedOrder.currency) }}</strong></div>
            <div><span>商品 ID</span><strong>{{ selectedOrder.product_id ?? '--' }}</strong></div>
            <div><span>套餐 ID</span><strong>{{ selectedOrder.product_plan_id ?? '--' }}</strong></div>
            <div><span>支付时间</span><strong>{{ formatDateTime(selectedOrder.paid_at) }}</strong></div>
            <div><span>取消时间</span><strong>{{ formatDateTime(selectedOrder.cancelled_at) }}</strong></div>
            <div><span>失败时间</span><strong>{{ formatDateTime(selectedOrder.failed_at) }}</strong></div>
            <div class="wide"><span>备注</span><strong>{{ selectedOrder.remark || '无' }}</strong></div>
          </div>

          <h4 class="section-title">订单明细</h4>
          <el-table :data="selectedOrder.items || []" border>
            <el-table-column prop="product_id" label="商品 ID" />
            <el-table-column prop="product_plan_id" label="套餐 ID" />
            <el-table-column prop="quantity" label="数量" />
            <el-table-column label="单价">
              <template #default="{ row }">{{ displayAmount(row.unit_price, selectedOrder?.currency) }}</template>
            </el-table-column>
            <el-table-column label="小计">
              <template #default="{ row }">{{ displayAmount(row.total_price, selectedOrder?.currency) }}</template>
            </el-table-column>
          </el-table>
        </template>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.order-page { padding: 0; }
.page-header {
  margin-bottom: 16px;
}
.page-title-text {
  margin: 0;
  color: var(--mc-text);
  font-size: 18px;
  font-weight: 700;
}
.page-subtitle {
  margin: 4px 0 0;
  color: var(--mc-text-muted);
  font-size: 12px;
}
.filter-card {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  padding: 14px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  background: var(--mc-surface);
}
.filter-card .el-select {
  width: 140px;
}
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
.detail-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 18px;
}
.detail-grid div {
  display: grid;
  gap: 6px;
  padding: 12px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
}
.detail-grid .wide {
  grid-column: 1 / -1;
}
.detail-grid span {
  color: var(--mc-text-muted);
  font-size: 12px;
}
.detail-grid strong,
.section-title {
  color: var(--mc-text);
}
@media (max-width: 900px) {
  .filter-card,
  .detail-grid {
    grid-template-columns: 1fr;
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
