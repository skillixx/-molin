<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Goods, Medal, Refresh, Star } from '@element-plus/icons-vue'
import { listProducts } from '@/api/product'
import { getMyMembership, listMembershipBenefits, listMembershipLevels } from '@/api/membership'
import type { Product } from '@/types/product'
import type { MembershipBenefit, MembershipLevel, MyMembership } from '@/types/membership'
import { formatDateTime, membershipStatusLabel } from '@/utils/display'
import { useRouter } from 'vue-router'

const router = useRouter()
const loading = ref(false)
const productLoading = ref(false)
const levels = ref<MembershipLevel[]>([])
const myMembership = ref<MyMembership | null>(null)
const benefitsByLevel = ref<Record<number, MembershipBenefit[]>>({})
const membershipProducts = ref<Product[]>([])

const currentLevelId = computed(() => myMembership.value?.level_id)
const hasMembership = computed(() => myMembership.value?.status === 'active')

onMounted(async () => {
  await Promise.all([fetchMembershipData(), fetchMembershipProducts()])
})

async function fetchMembershipData() {
  loading.value = true
  try {
    const [levelRes, myRes] = await Promise.all([listMembershipLevels(), getMyMembership()])
    levels.value = levelRes.items
    // 后端约定无会员时 membership 为 null，页面所有展示都围绕这个字段兜底。
    myMembership.value = myRes.membership
    await fetchBenefits(levelRes.items)
  } finally {
    loading.value = false
  }
}

async function fetchBenefits(items: MembershipLevel[]) {
  const entries = await Promise.all(
    items.map(async (level) => {
      try {
        const res = await listMembershipBenefits(level.id)
        return [level.id, res.items] as const
      } catch {
        // 权益接口可能因等级被下架返回 404，用户端不暴露异常细节，只展示空权益。
        return [level.id, []] as const
      }
    }),
  )
  benefitsByLevel.value = Object.fromEntries(entries)
}

async function fetchMembershipProducts() {
  productLoading.value = true
  try {
    // 会员开通/续费只能走商品购买流程，这里只查 membership 类型商品作为入口。
    const res = await listProducts({ product_type: 'membership', page: 1, page_size: 20 })
    membershipProducts.value = res.items.filter((item) => item.status === 'active')
  } finally {
    productLoading.value = false
  }
}

function benefitSummary(benefit: MembershipBenefit) {
  try {
    const parsed = JSON.parse(benefit.benefit_value) as Record<string, unknown>
    const text = Object.entries(parsed)
      .map(([key, value]) => `${key}: ${String(value)}`)
      .join('，')
    return text || '已配置权益'
  } catch {
    // benefit_value 是后端约定的 JSON 字符串，解析失败时展示原文，避免页面白屏。
    return benefit.benefit_value || '已配置权益'
  }
}

function goBuyMembership(product?: Product) {
  const target = product || membershipProducts.value[0]
  if (!target) {
    ElMessage.warning('暂无可购买的会员商品，请稍后再试')
    return
  }
  router.push(`/marketplace/${target.id}`)
}
</script>

<template>
  <div class="membership-page">
    <div class="page-container">
      <section class="page-header">
        <div>
          <span class="page-kicker">会员中心</span>
          <h2 class="page-title">会员权益</h2>
          <p class="page-subtitle">查看当前会员、等级权益，并通过会员商品完成开通或续费。</p>
        </div>
        <el-button :icon="Refresh" :loading="loading || productLoading" @click="fetchMembershipData(); fetchMembershipProducts()">刷新</el-button>
      </section>

      <section class="member-hero glass-card" v-loading="loading">
        <div class="member-badge">
          <el-icon><Medal /></el-icon>
        </div>
        <div class="member-info">
          <span class="member-kicker">{{ hasMembership ? '当前会员' : '暂无有效会员' }}</span>
          <h3>{{ myMembership?.level_name || '开通会员，解锁更多权益' }}</h3>
          <p v-if="myMembership">
            状态：{{ membershipStatusLabel(myMembership.status) }} · 开始 {{ formatDateTime(myMembership.started_at) }} · 到期 {{ formatDateTime(myMembership.expires_at) }}
          </p>
          <p v-else>会员状态由后端实时返回，开通或续费成功后刷新即可看到最新到期时间。</p>
        </div>
        <el-button type="primary" :loading="productLoading" @click="goBuyMembership()">
          {{ hasMembership ? '续费会员' : '开通会员' }}
        </el-button>
      </section>

      <section class="level-section">
        <div class="section-title">
          <h3>会员等级</h3>
          <span>权益来自公开权益端点，仅展示已启用权益</span>
        </div>

        <div v-loading="loading" class="level-grid">
          <article
            v-for="level in levels"
            :key="level.id"
            class="level-card glass-card"
            :class="{ current: currentLevelId === level.id }"
          >
            <div class="level-top">
              <div>
                <span class="level-code">{{ level.level_code }}</span>
                <h4>{{ level.name }}</h4>
              </div>
              <el-tag v-if="currentLevelId === level.id" type="success">当前等级</el-tag>
            </div>
            <p class="level-desc">{{ level.description || '暂无等级说明' }}</p>
            <div class="benefit-list">
              <div v-for="benefit in benefitsByLevel[level.id] || []" :key="benefit.id" class="benefit-item">
                <el-icon><Star /></el-icon>
                <div>
                  <strong>{{ benefit.benefit_type }}</strong>
                  <span>{{ benefitSummary(benefit) }}</span>
                </div>
              </div>
              <div v-if="(benefitsByLevel[level.id] || []).length === 0" class="benefit-empty">暂无公开权益</div>
            </div>
          </article>
        </div>

        <el-empty v-if="!loading && levels.length === 0" description="暂无可用会员等级" />
      </section>

      <section class="product-section glass-card">
        <div class="section-title">
          <h3>会员商品</h3>
          <span>开通和续费统一跳转商品购买流程</span>
        </div>
        <div v-loading="productLoading" class="product-list">
          <button
            v-for="product in membershipProducts"
            :key="product.id"
            class="product-entry"
            type="button"
            @click="goBuyMembership(product)"
          >
            <el-icon><Goods /></el-icon>
            <span>{{ product.name }}</span>
            <small>{{ product.product_code }}</small>
          </button>
        </div>
        <el-empty v-if="!productLoading && membershipProducts.length === 0" description="暂无会员商品" />
      </section>
    </div>
  </div>
</template>

<style scoped>
.membership-page { padding: 34px 0 0; }
.page-header, .member-hero, .product-section {
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 44%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.56);
  box-shadow: var(--shadow-card);
}
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 16px;
  padding: 24px;
  margin-bottom: 18px;
}
.page-kicker, .member-kicker, .level-code { color: var(--color-accent); font-size: 13px; font-weight: 700; }
.page-title { margin-bottom: 8px; }
.member-hero {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 18px;
  padding: 24px;
  margin-bottom: 22px;
}
.member-badge {
  width: 62px;
  height: 62px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: rgba(34, 211, 238, 0.12);
  color: var(--color-primary);
}
.member-badge .el-icon { font-size: 32px; }
.member-info h3 { margin: 6px 0 8px; color: var(--color-text); font-size: 22px; }
.member-info p, .section-title span, .level-desc, .benefit-item span, .benefit-empty, .product-entry small {
  color: var(--color-text-muted);
  line-height: 1.7;
}
.level-section { margin-bottom: 20px; }
.section-title {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 14px;
  margin: 0 0 14px;
}
.section-title h3 { color: var(--color-text); font-size: 18px; }
.level-grid {
  min-height: 180px;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
}
.level-card {
  padding: 20px;
  border-radius: 8px;
  border: 1px solid var(--color-border);
}
.level-card.current {
  border-color: rgba(52, 211, 153, 0.58);
  box-shadow: 0 18px 48px rgba(16, 185, 129, 0.12);
}
.level-top {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}
.level-top h4 { margin-top: 6px; color: var(--color-text); font-size: 18px; }
.level-desc { min-height: 46px; margin-bottom: 14px; }
.benefit-list { display: grid; gap: 10px; }
.benefit-item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.03);
}
.benefit-item .el-icon { color: var(--color-accent-warm); margin-top: 3px; }
.benefit-item strong { display: block; color: var(--color-text); margin-bottom: 2px; }
.benefit-empty { padding: 12px; border: 1px dashed var(--color-border); border-radius: 8px; text-align: center; }
.product-section { padding: 20px; }
.product-list {
  min-height: 68px;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}
.product-entry {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 4px 10px;
  align-items: center;
  padding: 14px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.62);
  color: var(--color-text);
  text-align: left;
  cursor: pointer;
}
.product-entry:hover { border-color: var(--color-border-hover); }
.product-entry .el-icon { grid-row: span 2; color: var(--color-primary); }
@media (max-width: 900px) {
  .page-header, .member-hero, .section-title {
    grid-template-columns: 1fr;
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
