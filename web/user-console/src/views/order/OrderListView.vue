<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { listMyOrders } from '@/api/order'
import type { Order } from '@/types/order'
import { displayAmount, formatDateTime, orderStatusLabel, orderTypeLabel } from '@/utils/display'

const loading = ref(false)
const orders = ref<Order[]>([])
const query = reactive({
  status: '',
  order_type: '',
  dates: [] as string[],
  page: 1,
  page_size: 20,
  total: 0,
})

onMounted(fetchOrders)

async function fetchOrders() {
  loading.value = true
  try {
    const res = await listMyOrders({
      status: query.status || undefined,
      order_type: query.order_type || undefined,
      created_from: query.dates[0],
      created_to: query.dates[1],
      page: query.page,
      page_size: query.page_size,
    })
    orders.value = res.items
    query.page = res.page
    query.page_size = res.page_size
    query.total = res.total
  } finally {
    loading.value = false
  }
}

function search() {
  query.page = 1
  fetchOrders()
}

function reset() {
  query.status = ''
  query.order_type = ''
  query.dates = []
  search()
}

function handlePageChange(page: number) {
  query.page = page
  fetchOrders()
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
    <div class="page-container">
      <div class="page-header">
        <h2 class="page-title">我的订单</h2>
        <router-link to="/marketplace"><el-button type="primary">去购买商品</el-button></router-link>
      </div>

      <div class="filter-bar glass-card">
        <el-select v-model="query.status" clearable placeholder="订单状态">
          <el-option label="待支付" value="pending" />
          <el-option label="已支付" value="paid" />
          <el-option label="已取消" value="cancelled" />
          <el-option label="支付失败" value="failed" />
        </el-select>
        <el-select v-model="query.order_type" clearable placeholder="订单类型">
          <el-option label="商品订单" value="product" />
          <el-option label="充值订单" value="recharge" />
        </el-select>
        <el-date-picker
          v-model="query.dates"
          type="daterange"
          value-format="YYYY-MM-DD"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
        />
        <el-button type="primary" :loading="loading" @click="search">查询</el-button>
        <el-button @click="reset">重置</el-button>
      </div>

      <el-table v-loading="loading" :data="orders" class="data-table" border>
        <el-table-column prop="order_no" label="订单号" min-width="190" />
        <el-table-column label="类型" width="110">
          <template #default="{ row }">{{ orderTypeLabel(row.order_type) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="110">
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
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <router-link :to="`/orders/${row.id}`"><el-button type="primary" text>详情</el-button></router-link>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          background
          layout="prev, pager, next, total"
          :current-page="query.page"
          :page-size="query.page_size"
          :total="query.total"
          @current-change="handlePageChange"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.order-page { padding: 32px 24px; }
.page-container { max-width: 1280px; margin: 0 auto; }
.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}
.page-title { color: var(--color-text); font-size: 26px; }
.filter-bar {
  display: grid;
  grid-template-columns: 140px 140px minmax(260px, 1fr) auto auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 16px;
  border-radius: 8px;
}
.data-table { width: 100%; }
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}
@media (max-width: 900px) {
  .page-header,
  .filter-bar {
    grid-template-columns: 1fr;
    flex-direction: column;
  }
}
</style>
