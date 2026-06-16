<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Refresh } from '@element-plus/icons-vue'
import { getProduct, getProductPlans } from '@/api/product'
import type { Product, ProductPlan } from '@/types/product'
import PurchaseDialog from './PurchaseDialog.vue'
import {
  billingTypeLabel,
  displayAmount,
  formatDateTime,
  isUnpriced,
  productStatusLabel,
} from '@/utils/display'

const route = useRoute()
const router = useRouter()
const productId = computed(() => Number(route.params.id))
const loading = ref(false)
const product = ref<Product | null>(null)
const plans = ref<ProductPlan[]>([])
const selectedPlan = ref<ProductPlan | null>(null)
const purchaseVisible = ref(false)

onMounted(fetchDetail)

async function fetchDetail() {
  if (!productId.value) return
  loading.value = true
  try {
    const [detail, planPage] = await Promise.all([
      getProduct(productId.value),
      getProductPlans(productId.value),
    ])
    product.value = detail.product
    plans.value = planPage.items.length > 0 ? planPage.items : detail.plans
  } finally {
    loading.value = false
  }
}

function openPurchase(plan: ProductPlan) {
  selectedPlan.value = plan
  purchaseVisible.value = true
}
</script>

<template>
  <div class="detail-page">
    <div class="page-container">
      <div class="page-header">
        <el-button :icon="ArrowLeft" text @click="router.push('/marketplace')">返回商品市场</el-button>
        <el-button :icon="Refresh" :loading="loading" @click="fetchDetail">刷新</el-button>
      </div>

      <div v-loading="loading" class="detail-layout">
        <section v-if="product" class="product-panel glass-card">
          <div class="product-meta">
            <span>{{ product.product_type }}</span>
            <el-tag :type="product.status === 'active' ? 'success' : 'info'">
              {{ productStatusLabel(product.status) }}
            </el-tag>
          </div>
          <h2>{{ product.name }}</h2>
          <p class="product-code">{{ product.product_code }}</p>
          <p class="product-desc">{{ product.description || '暂无商品说明' }}</p>
          <div class="info-grid">
            <div>
              <span>商品 ID</span>
              <strong>{{ product.id }}</strong>
            </div>
            <div>
              <span>创建时间</span>
              <strong>{{ formatDateTime(product.created_at) }}</strong>
            </div>
            <div>
              <span>更新时间</span>
              <strong>{{ formatDateTime(product.updated_at) }}</strong>
            </div>
          </div>
        </section>

        <section class="plans-panel">
          <div class="section-title">可选套餐</div>
          <div class="plan-list">
            <div v-for="plan in plans" :key="plan.id" class="plan-card glass-card">
              <div class="plan-head">
                <div>
                  <h3>{{ plan.name }}</h3>
                  <p>{{ plan.plan_code }}</p>
                </div>
                <el-tag :type="plan.status === 'active' ? 'success' : 'info'" size="small">
                  {{ productStatusLabel(plan.status) }}
                </el-tag>
              </div>
              <div class="plan-price" :class="{ muted: isUnpriced(plan.user_price) }">
                {{ isUnpriced(plan.user_price) ? '未定价' : displayAmount(plan.user_price, plan.currency) }}
              </div>
              <div class="plan-info">
                <span>计费方式：{{ billingTypeLabel(plan.billing_type) }}</span>
                <span>有效期：{{ plan.duration_days ?? '不限' }} 天</span>
              </div>
              <pre v-if="plan.quota_json" class="quota-json">{{ plan.quota_json }}</pre>
              <el-button
                type="primary"
                :disabled="plan.status !== 'active' || isUnpriced(plan.user_price)"
                @click="openPurchase(plan)"
              >
                {{ isUnpriced(plan.user_price) ? '暂不可购买' : '购买套餐' }}
              </el-button>
            </div>
          </div>
          <el-empty v-if="!loading && plans.length === 0" description="暂无可选套餐" />
        </section>
      </div>
    </div>

    <PurchaseDialog
      v-model="purchaseVisible"
      :product="product"
      :plan="selectedPlan"
      @success="fetchDetail"
    />
  </div>
</template>

<style scoped>
.detail-page { padding: 32px 24px; }
.page-container { max-width: 1180px; margin: 0 auto; }
.page-header {
  display: flex;
  justify-content: space-between;
  margin-bottom: 18px;
}
.detail-layout {
  display: grid;
  grid-template-columns: 360px minmax(0, 1fr);
  gap: 18px;
}
.product-panel,
.plan-card {
  padding: 22px;
  border-radius: 8px;
}
.product-meta,
.plan-head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.product-meta {
  color: var(--color-accent);
  font-size: 12px;
}
.product-panel h2 {
  margin-top: 18px;
  color: var(--color-text);
  font-size: 24px;
}
.product-code,
.plan-head p {
  margin-top: 6px;
  color: var(--color-text-disabled);
  font-size: 12px;
}
.product-desc {
  margin-top: 18px;
  color: var(--color-text-muted);
  line-height: 1.8;
}
.info-grid {
  display: grid;
  gap: 12px;
  margin-top: 22px;
}
.info-grid div {
  display: grid;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
}
.info-grid span,
.plan-info {
  color: var(--color-text-muted);
  font-size: 12px;
}
.info-grid strong {
  color: var(--color-text);
  font-size: 13px;
}
.section-title {
  margin-bottom: 12px;
  color: var(--color-text);
  font-size: 18px;
  font-weight: 700;
}
.plan-list {
  display: grid;
  gap: 14px;
}
.plan-head h3 {
  color: var(--color-text);
  font-size: 17px;
}
.plan-price {
  margin: 18px 0 10px;
  color: var(--color-accent);
  font-size: 24px;
  font-weight: 800;
}
.plan-price.muted {
  color: var(--color-warning);
}
.plan-info {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}
.quota-json {
  max-height: 120px;
  overflow: auto;
  margin-bottom: 16px;
  padding: 10px;
  border-radius: 8px;
  color: var(--color-text-muted);
  background: rgba(255, 255, 255, 0.04);
}
@media (max-width: 900px) {
  .detail-layout {
    grid-template-columns: 1fr;
  }
}
</style>
