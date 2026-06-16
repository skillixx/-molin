<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { v4 as uuidv4 } from 'uuid'
import { purchaseProduct } from '@/api/product'
import { useWalletStore } from '@/stores/wallet'
import type { Product, ProductPlan } from '@/types/product'
import { displayAmount, getErrorCode, isUnpriced } from '@/utils/display'

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
  <el-dialog v-model="visible" title="确认购买" width="520px">
    <div v-if="product && plan" class="purchase-summary">
      <div class="summary-row">
        <span>商品</span>
        <strong>{{ product.name }}</strong>
      </div>
      <div class="summary-row">
        <span>套餐</span>
        <strong>{{ plan.name }}</strong>
      </div>
      <div class="summary-row">
        <span>价格</span>
        <strong>{{ isUnpriced(plan.user_price) ? '未定价' : displayAmount(plan.user_price, plan.currency) }}</strong>
      </div>
      <el-form label-width="88px">
        <el-form-item label="购买数量">
          <el-input-number v-model="quantity" :min="1" :max="100" style="width: 100%" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="remark" maxlength="120" show-word-limit placeholder="可填写购买备注" />
        </el-form-item>
      </el-form>
      <p class="tip">提交时会使用同一个幂等键保护本次购买，重试不会重复扣费。</p>
    </div>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :disabled="!canSubmit" :loading="submitting" @click="handlePurchase">
        确认购买
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.purchase-summary {
  display: grid;
  gap: 14px;
}
.summary-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: var(--color-text-muted);
  font-size: 13px;
}
.summary-row strong {
  color: var(--color-text);
  text-align: right;
}
.tip {
  color: var(--color-text-disabled);
  font-size: 12px;
  line-height: 1.6;
}
</style>
