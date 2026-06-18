<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Check, ShoppingCartFull, WarningFilled } from '@element-plus/icons-vue'
import { v4 as uuidv4 } from 'uuid'
import { purchaseProduct } from '@/api/product'
import { useWalletStore } from '@/stores/wallet'
import type { Product, ProductPlan } from '@/types/product'
import { billingTypeLabel, displayAmount, getErrorCode, isUnpriced } from '@/utils/display'

const props = defineProps<{
  modelValue: boolean
  product: Product | null
  plan: ProductPlan | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  success: []
}>()

const router = useRouter()
const walletStore = useWalletStore()
const quantity = ref(1)
const remark = ref('')
const submitting = ref(false)
const idempotencyKey = ref('')

const visible = computed({
  get: () => props.modelValue,
  set: value => emit('update:modelValue', value),
})

watch(
  () => props.modelValue,
  value => {
    if (value) {
      quantity.value = 1
      remark.value = ''
      idempotencyKey.value = uuidv4()
    }
  },
)

const canSubmit = computed(() => !!props.product && !!props.plan && !isUnpriced(props.plan.user_price))
const priceText = computed(() => {
  if (!props.plan || isUnpriced(props.plan.user_price)) return '暂不可购买'
  return displayAmount(props.plan.user_price, props.plan.currency)
})

async function handlePurchase() {
  if (!props.product || !props.plan || !canSubmit.value) return
  submitting.value = true
  try {
    const result = await purchaseProduct(
      props.product.id,
      {
        plan_id: props.plan.id,
        quantity: quantity.value,
        remark: remark.value || undefined,
      },
      idempotencyKey.value,
    )
    ElMessage.success(result.idempotent ? '该订单已购买，未重复扣费' : '购买成功')
    await walletStore.fetchBalance()
    emit('success')
    visible.value = false
    router.push(`/orders/${result.order_id}`)
  } catch (error) {
    const { code, status } = getErrorCode(error)
    if (code === 70001) {
      ElMessage.warning('请先完成实名认证后再购买')
      router.push({ path: '/identity', query: { redirect: router.currentRoute.value.fullPath } })
    } else if (code === 60001) {
      ElMessage.warning('钱包余额不足，请先充值')
      router.push('/wallet/recharge')
    } else if (code === 40003) {
      ElMessage.error('当前角色无购买权限')
    } else if (code === 40000) {
      ElMessage.error('该套餐暂不可购买')
    } else if (code === 50000 || status === 409) {
      ElMessage.warning('系统繁忙，请重试')
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog
    v-model="visible"
    class="purchase-dialog"
    width="560px"
    append-to-body
    :close-on-click-modal="!submitting"
    :close-on-press-escape="!submitting"
    :show-close="!submitting"
  >
    <template #header>
      <div class="purchase-dialog-head">
        <div class="purchase-dialog-icon">
          <el-icon><ShoppingCartFull /></el-icon>
        </div>
        <div>
          <h3>确认购买</h3>
          <p>请核对商品、套餐和支付信息，提交后将直接完成购买。</p>
        </div>
      </div>
    </template>

    <div v-if="product && plan" class="purchase-summary">
      <div class="product-block">
        <div>
          <span class="block-kicker">墨灵商品</span>
          <h4>{{ product.name }}</h4>
          <p>{{ product.product_code }}</p>
        </div>
        <span class="product-type">{{ product.product_type }}</span>
      </div>

      <div class="summary-grid">
        <div class="summary-card">
          <span>套餐</span>
          <strong>{{ plan.name }}</strong>
          <small>{{ plan.plan_code }}</small>
        </div>
        <div class="summary-card">
          <span>计费方式</span>
          <strong>{{ billingTypeLabel(plan.billing_type) }}</strong>
          <small>有效期 {{ plan.duration_days ?? '不限' }} 天</small>
        </div>
      </div>

      <div class="price-panel" :class="{ disabled: isUnpriced(plan.user_price) }">
        <div>
          <span>应付单价</span>
          <strong>{{ priceText }}</strong>
        </div>
        <span class="pay-method-badge" :class="{ disabled: isUnpriced(plan.user_price) }">
          <span class="pay-method-dot" aria-hidden="true"></span>
          {{ isUnpriced(plan.user_price) ? '未配置价格' : '钱包支付' }}
        </span>
      </div>

      <div class="purchase-form">
        <label class="field-label">购买数量</label>
        <el-input-number v-model="quantity" :min="1" :max="100" />

        <label class="field-label">购买备注</label>
        <el-input
          v-model="remark"
          class="remark-input"
          maxlength="120"
          show-word-limit
          placeholder="可填写购买备注"
        />
      </div>

      <div class="purchase-tip">
        <el-icon><WarningFilled /></el-icon>
        <span>本次购买使用幂等键保护，网络重试不会重复扣费；购买成功后会跳转到订单详情。</span>
      </div>
    </div>

    <template #footer>
      <div class="purchase-footer">
        <el-button :disabled="submitting" @click="visible = false">返回</el-button>
        <el-button
          class="purchase-confirm-btn"
          type="primary"
          :disabled="!canSubmit"
          :loading="submitting"
          @click="handlePurchase"
        >
          <el-icon><Check /></el-icon>
          确认购买
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
:global(.purchase-dialog) {
  border: 1px solid rgba(34, 211, 238, 0.24);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 38%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 34%),
    rgba(7, 11, 18, 0.96);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.42);
}

:global(.purchase-dialog .el-dialog__header) {
  margin: 0;
  padding: 22px 22px 0;
}

:global(.purchase-dialog .el-dialog__body) {
  padding: 18px 22px 0;
}

:global(.purchase-dialog .el-dialog__footer) {
  padding: 18px 22px 22px;
}

.purchase-dialog-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.purchase-dialog-icon {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  border: 1px solid rgba(34, 211, 238, 0.34);
  border-radius: 8px;
  color: #dffbff;
  background: rgba(34, 211, 238, 0.12);
  font-size: 20px;
}

.purchase-dialog-head h3 {
  margin: 0;
  color: var(--color-text);
  font-size: 18px;
  font-weight: 800;
}

.purchase-dialog-head p {
  margin: 6px 0 0;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

.purchase-summary {
  display: grid;
  gap: 12px;
}

.product-block {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 16px;
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.58);
}

.block-kicker,
.summary-card span,
.price-panel span,
.field-label {
  color: var(--color-text-muted);
  font-size: 12px;
}

.product-block h4 {
  margin: 6px 0 0;
  color: var(--color-text);
  font-size: 17px;
  line-height: 1.35;
}

.product-block p,
.summary-card small {
  margin: 6px 0 0;
  color: var(--color-text-disabled);
  font-size: 12px;
}

.product-type {
  flex-shrink: 0;
  padding: 5px 9px;
  border: 1px solid rgba(34, 211, 238, 0.26);
  border-radius: 8px;
  color: var(--color-primary);
  background: rgba(34, 211, 238, 0.08);
  font-size: 12px;
  font-weight: 700;
}

.summary-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.summary-card {
  display: grid;
  gap: 6px;
  min-width: 0;
  padding: 13px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 8px;
  background: rgba(2, 6, 23, 0.38);
}

.summary-card strong {
  overflow: hidden;
  color: var(--color-text);
  font-size: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.price-panel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 16px;
  border: 1px solid rgba(52, 211, 153, 0.26);
  border-radius: 8px;
  background: linear-gradient(135deg, rgba(52, 211, 153, 0.14), rgba(15, 23, 42, 0.44));
}

.price-panel.disabled {
  border-color: rgba(148, 163, 184, 0.16);
  background: rgba(15, 23, 42, 0.5);
}

.price-panel div {
  display: grid;
  gap: 6px;
}

.price-panel strong {
  color: var(--color-accent);
  font-size: 26px;
  font-weight: 900;
  line-height: 1;
}

.price-panel.disabled strong {
  color: var(--color-text-muted);
}

.pay-method-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  min-width: 88px;
  height: 32px;
  padding: 0 12px;
  border: 1px solid rgba(52, 211, 153, 0.58);
  border-radius: 8px;
  color: #ecfdf5;
  background: rgba(5, 150, 105, 0.28);
  font-size: 13px;
  font-weight: 800;
  line-height: 1;
  white-space: nowrap;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.1);
}

.pay-method-badge.disabled {
  border-color: rgba(148, 163, 184, 0.34);
  color: var(--color-text-muted);
  background: rgba(15, 23, 42, 0.62);
}

.pay-method-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #34d399;
  box-shadow: 0 0 0 3px rgba(52, 211, 153, 0.18);
}

.pay-method-badge.disabled .pay-method-dot {
  background: var(--color-text-muted);
  box-shadow: 0 0 0 3px rgba(148, 163, 184, 0.14);
}

.purchase-form {
  display: grid;
  gap: 8px;
  padding: 14px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.44);
}

.purchase-form :deep(.el-input-number) {
  width: 100%;
}

.remark-input {
  margin-top: 2px;
}

.purchase-tip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px;
  border: 1px solid rgba(251, 191, 36, 0.18);
  border-radius: 8px;
  color: #facc15;
  background: rgba(251, 191, 36, 0.08);
  font-size: 12px;
  line-height: 1.7;
}

.purchase-tip .el-icon {
  margin-top: 3px;
  flex-shrink: 0;
}

.purchase-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.purchase-confirm-btn {
  min-width: 128px;
  border: none;
  background: linear-gradient(135deg, rgba(34, 211, 238, 0.95), rgba(52, 211, 153, 0.9)) !important;
  color: #041016 !important;
  font-weight: 800;
  box-shadow: 0 12px 26px rgba(34, 211, 238, 0.2);
}

.purchase-confirm-btn:hover {
  background: linear-gradient(135deg, rgba(103, 232, 249, 0.98), rgba(74, 222, 128, 0.94)) !important;
}

@media (max-width: 620px) {
  .summary-grid {
    grid-template-columns: 1fr;
  }

  .price-panel,
  .product-block,
  .purchase-footer {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
