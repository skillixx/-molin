<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Key, Refresh, ShoppingCart, Wallet } from '@element-plus/icons-vue'
import { getProductPlans, listProducts, purchaseProduct } from '@/api/product'
import type { Product, ProductPlan } from '@/types/product'

const router = useRouter()

const loading = ref(false)
const buying = ref(false)
const product = ref<Product | null>(null)
const plans = ref<ProductPlan[]>([])
const selectedPlanId = ref<number | null>(null)
const remark = ref('购买 Token 预付套餐')
const idempotencyKey = ref('')

const selectedPlan = computed(() => plans.value.find((item) => item.id === selectedPlanId.value))
const canBuy = computed(() => !!product.value && !!selectedPlan.value && selectedPlan.value.user_price !== '-1')

onMounted(fetchTokenProduct)

async function fetchTokenProduct() {
  loading.value = true
  try {
    const productRes = await listProducts({ keyword: 'token-api', page: 1, page_size: 100 })
    product.value = productRes.items.find((item) => item.product_code === 'token-api') || null
    if (!product.value) {
      plans.value = []
      return
    }

    const planRes = await getProductPlans(product.value.id, { page: 1, page_size: 100 })
    plans.value = planRes.items.filter((item) => item.status === 'active')
    selectedPlanId.value = plans.value.find((item) => item.user_price !== '-1')?.id || null
  } finally {
    loading.value = false
  }
}

function quotaLabel(plan: ProductPlan) {
  const quota = readQuota(plan)
  if (!quota) return '额度以套餐配置为准'
  const quotaText = formatTokenQuota(quota.quota_total)
  const unitText = quota.quota_unit ? quota.quota_unit.replace('token', 'Token') : 'Token'
  const days = quota.valid_days || plan.duration_days
  return `${quotaText} ${unitText}${days ? ` / 有效期 ${days} 天` : ''}`
}

function readQuota(plan: ProductPlan): { quota_total?: string; quota_unit?: string; valid_days?: number } | null {
  if (!plan.quota_json) return null
  try {
    return JSON.parse(plan.quota_json)
  } catch {
    return null
  }
}

function formatTokenQuota(value?: string) {
  if (!value) return '--'
  const tokenCount = Number(value)
  if (!Number.isFinite(tokenCount)) return value
  if (tokenCount >= 10000) return `${Math.floor(tokenCount / 10000)} 万`
  return value
}

function priceLabel(plan: ProductPlan) {
  if (plan.user_price === '-1') return '暂不可购买'
  return plan.user_price === '0' ? '免费' : `¥ ${plan.user_price}`
}

function buildIdempotencyKey() {
  if (crypto.randomUUID) return crypto.randomUUID()
  return `token-package-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

async function handlePurchase() {
  if (!product.value || !selectedPlan.value || !canBuy.value) return
  if (!idempotencyKey.value) idempotencyKey.value = buildIdempotencyKey()

  buying.value = true
  try {
    const res = await purchaseProduct(
      product.value.id,
      { plan_id: selectedPlan.value.id, quantity: 1, remark: remark.value },
      idempotencyKey.value,
    )
    ElMessage.success(res.idempotent ? '套餐购买已确认' : '套餐购买成功')
    idempotencyKey.value = ''
    router.push('/api-keys')
  } catch (err) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 70001) router.push({ path: '/identity', query: { redirect: '/token/packages' } })
    if (code === 60001) router.push('/wallet/recharge')
  } finally {
    buying.value = false
  }
}
</script>

<template>
  <div class="token-package-page">
    <div class="page-container">
      <section class="package-hero glass-card">
        <div>
          <span class="page-kicker">Token 套餐</span>
          <h2>购买预付 Token 套餐</h2>
          <p>购买后生成 Token 额度权益，可前往 API 密钥页签发 prepaid key。</p>
        </div>
        <div class="hero-actions">
          <el-button :icon="Refresh" :loading="loading" @click="fetchTokenProduct">刷新</el-button>
          <el-button :icon="Wallet" @click="router.push('/wallet/recharge')">钱包充值</el-button>
          <el-button type="primary" :icon="Key" @click="router.push('/api-keys')">API 密钥</el-button>
        </div>
      </section>

      <el-alert
        class="package-alert"
        type="info"
        show-icon
        :closable="false"
        title="购买要求已完成实名认证且钱包余额充足。Agent、Skill、插件本身免费，只有模型 Token 调用会计费。"
      />

      <section class="plans-grid" v-loading="loading">
        <div v-if="!loading && !product" class="empty-card glass-card">
          未找到 product_code=token-api 的商品，请联系后端补充商品配置。
        </div>

        <button
          v-for="plan in plans"
          :key="plan.id"
          class="plan-card glass-card"
          :class="{ active: selectedPlanId === plan.id, disabled: plan.user_price === '-1' }"
          type="button"
          :disabled="plan.user_price === '-1'"
          @click="selectedPlanId = plan.id"
        >
          <div class="plan-top">
            <h3>{{ plan.name }}</h3>
            <el-tag v-if="plan.user_price === '-1'" type="info">未配置价格</el-tag>
            <el-tag v-else type="success">可购买</el-tag>
          </div>
          <div class="plan-quota">{{ quotaLabel(plan) }}</div>
          <div class="plan-price">{{ priceLabel(plan) }}</div>
        </button>

        <div v-if="!loading && product && plans.length === 0" class="empty-card glass-card">
          暂无可购买套餐
        </div>
      </section>

      <section class="checkout-bar glass-card">
        <div>
          <div class="checkout-title">当前选择</div>
          <div class="checkout-desc">
            {{ selectedPlan ? `${selectedPlan.name} · ${quotaLabel(selectedPlan)}` : '请选择一个套餐' }}
          </div>
        </div>
        <div class="checkout-actions">
          <el-input v-model="remark" class="remark-input" maxlength="80" placeholder="购买备注" />
          <el-button
            type="primary"
            :icon="ShoppingCart"
            :loading="buying"
            :disabled="!canBuy"
            @click="handlePurchase"
          >
            确认购买
          </el-button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.token-package-page { padding: 34px 0 42px; }
.package-hero {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 20px;
  padding: 24px;
}
.page-kicker { color: var(--color-accent); font-size: 13px; font-weight: 700; }
.package-hero h2 { margin: 8px 0 6px; }
.package-hero p { color: var(--color-text-muted); }
.hero-actions { display: flex; gap: 10px; flex-wrap: wrap; justify-content: flex-end; }
.package-alert { margin: 18px 0; }
.plans-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
  min-height: 160px;
}
.plan-card {
  text-align: left;
  padding: 20px;
  border: 1px solid var(--color-border);
  cursor: pointer;
  transition: border-color 0.2s, transform 0.2s, background 0.2s;
}
.plan-card:hover,
.plan-card.active {
  border-color: rgba(34, 211, 238, 0.66);
  transform: translateY(-2px);
  background: rgba(22, 119, 255, 0.12);
}
.plan-card.disabled { opacity: 0.62; cursor: not-allowed; }
.plan-top { display: flex; justify-content: space-between; gap: 12px; align-items: center; }
.plan-top h3 { font-size: 18px; }
.plan-quota { margin-top: 18px; color: var(--color-text-muted); line-height: 1.7; }
.plan-price { margin-top: 18px; font-size: 28px; font-weight: 800; color: var(--color-text); }
.empty-card { padding: 28px; color: var(--color-text-muted); }
.checkout-bar {
  margin-top: 18px;
  padding: 18px 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.checkout-title { font-weight: 700; }
.checkout-desc { margin-top: 6px; color: var(--color-text-muted); }
.checkout-actions { display: flex; gap: 10px; align-items: center; }
.remark-input { width: 260px; }
@media (max-width: 800px) {
  .package-hero,
  .checkout-bar { flex-direction: column; align-items: stretch; }
  .hero-actions,
  .checkout-actions { justify-content: stretch; }
  .remark-input { width: 100%; }
}
</style>
