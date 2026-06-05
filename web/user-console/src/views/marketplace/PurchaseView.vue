<script setup lang="ts">
/**
 * 购买确认页（Week 2 实现）
 * 安全约定：进入本页前路由守卫已检查实名认证状态
 * 购买请求必须携带 Idempotency-Key（UUID v4）
 */
import { onMounted } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

// 二次校验实名认证（路由守卫之外的保险措施）
onMounted(() => {
  if (authStore.realNameStatus !== 'verified') {
    router.push('/identity')
  }
})

/**
 * 生成购买幂等键
 * TODO: Week 2 对接购买接口时使用
 */
function generateIdempotencyKey(userId: number, productId: string, planId: string) {
  return `${userId}-${productId}-${planId}-${Date.now()}-${uuidv4()}`
}

// 供 Week 2 使用的占位
const _productId = route.params.id
const _key = generateIdempotencyKey(authStore.currentUser?.id ?? 0, String(_productId), '')
console.debug('[PurchaseView] idempotencyKey ready:', _key)
</script>

<template>
  <div class="purchase-page">
    <div class="page-container">
      <h2 class="page-title">确认购买</h2>
      <div class="coming-soon glass-card">
        <p>🛒 购买流程功能 Week 2 上线</p>
        <p class="sub">购买前已完成实名认证状态校验</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.purchase-page { padding: 32px 24px; }
.page-container { max-width: 800px; margin: 0 auto; }
.page-title { font-size: 24px; font-weight: 600; color: var(--color-text); margin-bottom: 24px; }
.coming-soon { padding: 60px; text-align: center; color: var(--color-text-muted); font-size: 15px; }
.sub { font-size: 12px; margin-top: 8px; color: var(--color-text-disabled); }
</style>
