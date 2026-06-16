<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Search, Refresh } from '@element-plus/icons-vue'
import { listProducts } from '@/api/product'
import type { Product } from '@/types/product'
import { formatDateTime, productStatusLabel } from '@/utils/display'

const loading = ref(false)
const products = ref<Product[]>([])
const query = reactive({
  keyword: '',
  product_type: '',
  page: 1,
  page_size: 20,
  total: 0,
})

onMounted(fetchProducts)

async function fetchProducts() {
  loading.value = true
  try {
    const res = await listProducts({
      keyword: query.keyword || undefined,
      product_type: query.product_type || undefined,
      page: query.page,
      page_size: query.page_size,
    })
    products.value = res.items
    query.page = res.page
    query.page_size = res.page_size
    query.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  query.page = 1
  fetchProducts()
}

function handleReset() {
  query.keyword = ''
  query.product_type = ''
  query.page = 1
  fetchProducts()
}

function handlePageChange(page: number) {
  query.page = page
  fetchProducts()
}
</script>

<template>
  <div class="marketplace-page">
    <div class="page-container">
      <div class="page-header">
        <div>
          <h2 class="page-title">商品市场</h2>
          <p class="page-subtitle">选择适合当前业务的云资源、应用和服务能力</p>
        </div>
        <el-button :icon="Refresh" :loading="loading" @click="fetchProducts">刷新</el-button>
      </div>

      <div class="filter-bar glass-card">
        <el-input
          v-model="query.keyword"
          clearable
          placeholder="搜索商品名称或代码"
          :prefix-icon="Search"
          @keyup.enter="handleSearch"
        />
        <el-input
          v-model="query.product_type"
          clearable
          placeholder="商品类型"
          @keyup.enter="handleSearch"
        />
        <el-button type="primary" :loading="loading" @click="handleSearch">查询</el-button>
        <el-button @click="handleReset">重置</el-button>
      </div>

      <div v-loading="loading" class="product-grid">
        <router-link
          v-for="item in products"
          :key="item.id"
          class="product-card glass-card"
          :to="`/marketplace/${item.id}`"
        >
          <div class="card-top">
            <span class="product-type">{{ item.product_type }}</span>
            <el-tag :type="item.status === 'active' ? 'success' : 'info'" size="small">
              {{ productStatusLabel(item.status) }}
            </el-tag>
          </div>
          <h3 class="product-name">{{ item.name }}</h3>
          <p class="product-code">{{ item.product_code }}</p>
          <p class="product-desc">{{ item.description || '暂无商品说明' }}</p>
          <div class="card-footer">
            <span>创建时间 {{ formatDateTime(item.created_at) }}</span>
            <el-button type="primary" text>查看详情</el-button>
          </div>
        </router-link>
      </div>

      <el-empty v-if="!loading && products.length === 0" description="暂无可购买商品" />

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
.marketplace-page { padding: 32px 24px; }
.page-container { max-width: 1280px; margin: 0 auto; }
.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
}
.page-title {
  font-size: 26px;
  font-weight: 700;
  color: var(--color-text);
  margin-bottom: 8px;
}
.page-subtitle {
  color: var(--color-text-muted);
  font-size: 14px;
}
.filter-bar {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) 180px auto auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 18px;
  border-radius: 8px;
}
.product-grid {
  min-height: 220px;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
}
.product-card {
  display: flex;
  flex-direction: column;
  min-height: 230px;
  padding: 20px;
  border-radius: 8px;
  text-decoration: none;
}
.card-top,
.card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.product-type {
  color: var(--color-accent);
  font-size: 12px;
}
.product-name {
  margin-top: 16px;
  color: var(--color-text);
  font-size: 18px;
}
.product-code {
  margin-top: 6px;
  color: var(--color-text-disabled);
  font-size: 12px;
}
.product-desc {
  flex: 1;
  margin-top: 14px;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.7;
}
.card-footer {
  margin-top: 18px;
  color: var(--color-text-disabled);
  font-size: 12px;
}
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
}
@media (max-width: 760px) {
  .page-header,
  .filter-bar {
    grid-template-columns: 1fr;
    flex-direction: column;
  }
}
</style>
