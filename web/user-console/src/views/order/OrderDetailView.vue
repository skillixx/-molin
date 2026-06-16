<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft, Refresh } from '@element-plus/icons-vue'
import { v4 as uuidv4 } from 'uuid'
import { cancelOrder, getOrder, payOrder } from '@/api/order'
import { useWalletStore } from '@/stores/wallet'
import type { Order } from '@/types/order'
import {
  displayAmount,
  formatDateTime,
  getErrorCode,
  orderStatusLabel,
  orderTypeLabel,
} from '@/utils/display'

const route = useRoute()
const router = useRouter()
const walletStore = useWalletStore()
const orderId = computed(() => Number(route.params.id))
const loading = ref(false)
const paying = ref(false)
const cancelling = ref(false)
const order = ref<Order | null>(null)

const canWalletPay = computed(() => order.value?.status === 'pending' && order.value.order_type === 'product')
const canCancel = computed(() => order.value?.status === 'pending')

onMounted(fetchOrder)

async function fetchOrder() {
  if (!orderId.value) return
  loading.value = true
  try {
    order.value = await getOrder(orderId.value)
  } finally {
    loading.value = false
  }
}

async function handlePay() {
  if (!order.value || !canWalletPay.value) return
  paying.value = true
  try {
    const res = await payOrder(order.value.id, uuidv4())
    ElMessage.success(`支付成功，流水 ID：${res.wallet_transaction_id}`)
    await Promise.all([fetchOrder(), walletStore.fetchBalance()])
  } catch (error) {
    const { code } = getErrorCode(error)
    if (code === 60001) {
      ElMessage.warning('钱包余额不足，请先充值')
      router.push('/wallet/recharge')
    } else if (code === 60002) {
      ElMessage.warning('订单已支付，正在刷新状态')
      await fetchOrder()
    } else if (code === 40900) {
      ElMessage.warning('订单状态已变化，请刷新后重试')
      await fetchOrder()
    } else if (code === 40000) {
      ElMessage.error('该订单不支持钱包支付')
    } else if (code === 40004) {
      ElMessage.error('订单不存在')
    }
  } finally {
    paying.value = false
  }
}

async function handleCancel() {
  if (!order.value || !canCancel.value) return
  const reason = await ElMessageBox.prompt('请输入取消原因', '取消订单', {
    confirmButtonText: '确认取消',
    cancelButtonText: '返回',
    inputValue: '用户主动取消',
  }).catch(() => null)
  if (!reason) return
  cancelling.value = true
  try {
    await cancelOrder(order.value.id, reason.value)
    ElMessage.success('订单已取消')
    await fetchOrder()
  } finally {
    cancelling.value = false
  }
}
</script>

<template>
  <div class="order-detail-page">
    <div class="page-container">
      <div class="page-header">
        <el-button :icon="ArrowLeft" text @click="router.push('/orders')">返回订单列表</el-button>
        <el-button :icon="Refresh" :loading="loading" @click="fetchOrder">刷新</el-button>
      </div>

      <div v-loading="loading" class="detail-grid">
        <section v-if="order" class="detail-card glass-card">
          <div class="title-row">
            <div>
              <h2>{{ order.order_no }}</h2>
              <p>{{ orderTypeLabel(order.order_type) }}</p>
            </div>
            <el-tag>{{ orderStatusLabel(order.status) }}</el-tag>
          </div>

          <div class="amount">{{ displayAmount(order.amount, order.currency) }}</div>

          <div class="info-grid">
            <div><span>订单 ID</span><strong>{{ order.id }}</strong></div>
            <div><span>商品 ID</span><strong>{{ order.product_id ?? '--' }}</strong></div>
            <div><span>套餐 ID</span><strong>{{ order.product_plan_id ?? '--' }}</strong></div>
            <div><span>创建时间</span><strong>{{ formatDateTime(order.created_at) }}</strong></div>
            <div><span>支付时间</span><strong>{{ formatDateTime(order.paid_at) }}</strong></div>
            <div><span>取消时间</span><strong>{{ formatDateTime(order.cancelled_at) }}</strong></div>
          </div>

          <p class="remark">备注：{{ order.remark || '无' }}</p>

          <div class="actions">
            <el-button
              type="primary"
              :disabled="!canWalletPay"
              :loading="paying"
              @click="handlePay"
            >
              钱包支付
            </el-button>
            <el-button :disabled="!canCancel" :loading="cancelling" @click="handleCancel">取消订单</el-button>
          </div>
          <p v-if="order.order_type === 'recharge' && order.status === 'pending'" class="tip">
            充值订单需通过第三方支付链接完成，不支持钱包支付。
          </p>
        </section>

        <section v-if="order?.items?.length" class="items-card glass-card">
          <div class="section-title">订单明细</div>
          <el-table :data="order.items" border>
            <el-table-column prop="product_id" label="商品 ID" />
            <el-table-column prop="product_plan_id" label="套餐 ID" />
            <el-table-column prop="quantity" label="数量" />
            <el-table-column label="单价">
              <template #default="{ row }">{{ displayAmount(row.unit_price, order?.currency) }}</template>
            </el-table-column>
            <el-table-column label="小计">
              <template #default="{ row }">{{ displayAmount(row.total_price, order?.currency) }}</template>
            </el-table-column>
          </el-table>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.order-detail-page { padding: 32px 24px; }
.page-container { max-width: 1080px; margin: 0 auto; }
.page-header {
  display: flex;
  justify-content: space-between;
  margin-bottom: 18px;
}
.detail-grid {
  display: grid;
  gap: 16px;
}
.detail-card,
.items-card {
  padding: 24px;
  border-radius: 8px;
}
.title-row {
  display: flex;
  justify-content: space-between;
  gap: 16px;
}
.title-row h2 { color: var(--color-text); font-size: 22px; }
.title-row p { margin-top: 6px; color: var(--color-text-muted); font-size: 13px; }
.amount {
  margin: 22px 0;
  color: var(--color-accent);
  font-size: 32px;
  font-weight: 800;
}
.info-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
.info-grid div {
  display: grid;
  gap: 6px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
}
.info-grid span,
.remark,
.tip {
  color: var(--color-text-muted);
  font-size: 13px;
}
.info-grid strong { color: var(--color-text); }
.remark { margin-top: 16px; }
.actions {
  display: flex;
  gap: 12px;
  margin-top: 22px;
}
.tip { margin-top: 12px; color: var(--color-warning); }
.section-title {
  margin-bottom: 14px;
  color: var(--color-text);
  font-size: 18px;
  font-weight: 700;
}
@media (max-width: 760px) {
  .info-grid {
    grid-template-columns: 1fr;
  }
}
</style>
