<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getUserWallet, listAllTransactions, freezeUserWallet, listPaymentCallbacks } from '@/api/wallet-admin'
import type { PaymentCallback, Wallet, WalletTransaction } from '@/types/wallet-admin'
import type { Pagination } from '@/types/api'
import {
  displayAmount,
  formatDateTime,
  isPositiveAmount,
  txDirectionLabel,
  txTypeLabel,
} from '@/utils/display'

const walletLoading = ref(false)
const wallet = ref<Wallet | null>(null)
const walletQuery = reactive({ user_id: undefined as number | undefined })

const txLoading = ref(false)
const transactions = ref<WalletTransaction[]>([])
const txQuery = reactive({
  user_id: undefined as number | undefined,
  type: '',
  direction: '',
  dates: [] as string[],
})
const txPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const callbackLoading = ref(false)
const callbacks = ref<PaymentCallback[]>([])
const callbackQuery = reactive({ provider: '', status: '' })
const callbackPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const freezeDialogVisible = ref(false)
const freezing = ref(false)
const freezeForm = reactive({
  action: 'freeze' as 'freeze' | 'unfreeze',
  amount: '',
  reason: '',
})

onMounted(() => {
  fetchTransactions()
  fetchCallbacks()
})

async function fetchWallet() {
  if (!walletQuery.user_id) {
    ElMessage.warning('请输入用户 ID')
    return
  }
  walletLoading.value = true
  try {
    wallet.value = await getUserWallet(walletQuery.user_id)
  } finally {
    walletLoading.value = false
  }
}

function openFreezeDialog(action: 'freeze' | 'unfreeze') {
  if (!wallet.value) {
    ElMessage.warning('请先查询用户钱包')
    return
  }
  freezeForm.action = action
  freezeForm.amount = ''
  freezeForm.reason = ''
  freezeDialogVisible.value = true
}

async function submitFreeze() {
  if (!wallet.value) return
  if (!isPositiveAmount(freezeForm.amount)) {
    ElMessage.warning('金额必填且必须大于 0，最多 6 位小数')
    return
  }
  await ElMessageBox.confirm(
    `确认${freezeForm.action === 'freeze' ? '冻结' : '解冻'}用户 ${wallet.value.user_id} 的钱包金额 ${freezeForm.amount}？`,
    '确认钱包操作',
    { type: 'warning' }
  )
  freezing.value = true
  try {
    await freezeUserWallet(wallet.value.user_id, {
      action: freezeForm.action,
      amount: freezeForm.amount,
      reason: freezeForm.reason || undefined,
    })
    ElMessage.success(freezeForm.action === 'freeze' ? '钱包金额已冻结' : '钱包金额已解冻')
    freezeDialogVisible.value = false
    await Promise.all([fetchWallet(), fetchTransactions()])
  } finally {
    freezing.value = false
  }
}

async function fetchTransactions() {
  txLoading.value = true
  try {
    const res = await listAllTransactions({
      user_id: txQuery.user_id,
      type: txQuery.type || undefined,
      direction: txQuery.direction || undefined,
      created_from: txQuery.dates[0],
      created_to: txQuery.dates[1],
      page: txPagination.page,
      page_size: txPagination.page_size,
    })
    transactions.value = res.items
    txPagination.page = res.page
    txPagination.page_size = res.page_size
    txPagination.total = res.total
  } finally {
    txLoading.value = false
  }
}

function searchTransactions() {
  txPagination.page = 1
  fetchTransactions()
}

function handleTxPageChange(page: number) {
  txPagination.page = page
  fetchTransactions()
}

async function fetchCallbacks() {
  callbackLoading.value = true
  try {
    const res = await listPaymentCallbacks({
      provider: callbackQuery.provider || undefined,
      status: callbackQuery.status || undefined,
      page: callbackPagination.page,
      page_size: callbackPagination.page_size,
    })
    callbacks.value = res.items
    callbackPagination.page = res.page
    callbackPagination.page_size = res.page_size
    callbackPagination.total = res.total
  } finally {
    callbackLoading.value = false
  }
}

function searchCallbacks() {
  callbackPagination.page = 1
  fetchCallbacks()
}

function handleCallbackPageChange(page: number) {
  callbackPagination.page = page
  fetchCallbacks()
}
</script>

<template>
  <div class="wallet-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">钱包中心</h3>
        <p class="page-subtitle">查询用户钱包、全量流水和支付回调记录；冻结操作需要 wallet:manage 权限</p>
      </div>
    </div>

    <el-tabs>
      <el-tab-pane label="用户钱包">
        <div class="filter-card">
          <el-input-number v-model="walletQuery.user_id" :min="1" placeholder="用户 ID" />
          <el-button type="primary" :loading="walletLoading" @click="fetchWallet">查询钱包</el-button>
        </div>
        <div v-if="wallet" class="wallet-card">
          <div><span>钱包 ID</span><strong>{{ wallet.wallet_id }}</strong></div>
          <div><span>用户 ID</span><strong>{{ wallet.user_id }}</strong></div>
          <div><span>可用余额</span><strong>{{ displayAmount(wallet.balance_amount, wallet.currency) }}</strong></div>
          <div><span>冻结金额</span><strong>{{ displayAmount(wallet.frozen_amount, wallet.currency) }}</strong></div>
          <div class="wallet-actions">
            <el-button type="warning" @click="openFreezeDialog('freeze')">冻结</el-button>
            <el-button @click="openFreezeDialog('unfreeze')">解冻</el-button>
          </div>
        </div>
      </el-tab-pane>

      <el-tab-pane label="全量流水">
        <div class="filter-card">
          <el-input-number v-model="txQuery.user_id" :min="1" placeholder="用户 ID" />
          <el-select v-model="txQuery.type" clearable placeholder="类型">
            <el-option label="充值" value="recharge" />
            <el-option label="消费" value="consume" />
            <el-option label="退款" value="refund" />
            <el-option label="冻结" value="freeze" />
            <el-option label="解冻" value="unfreeze" />
          </el-select>
          <el-select v-model="txQuery.direction" clearable placeholder="方向">
            <el-option label="入账" value="in" />
            <el-option label="出账" value="out" />
          </el-select>
          <el-date-picker v-model="txQuery.dates" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" />
          <el-button type="primary" :loading="txLoading" @click="searchTransactions">查询</el-button>
        </div>
        <el-table :data="transactions" v-loading="txLoading" border>
          <el-table-column prop="id" label="流水 ID" width="100" />
          <el-table-column prop="user_id" label="用户 ID" width="100" />
          <el-table-column prop="wallet_id" label="钱包 ID" width="100" />
          <el-table-column label="类型" width="100"><template #default="{ row }">{{ txTypeLabel(row.type) }}</template></el-table-column>
          <el-table-column label="方向" width="100"><template #default="{ row }">{{ txDirectionLabel(row.direction) }}</template></el-table-column>
          <el-table-column label="金额" min-width="140"><template #default="{ row }">{{ displayAmount(row.amount) }}</template></el-table-column>
          <el-table-column label="变更后余额" min-width="150"><template #default="{ row }">{{ displayAmount(row.balance_after) }}</template></el-table-column>
          <el-table-column prop="related_order_id" label="关联订单" width="120" />
          <el-table-column prop="remark" label="备注" min-width="180" />
          <el-table-column label="创建时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination background layout="prev, pager, next, total" :current-page="txPagination.page" :page-size="txPagination.page_size" :total="txPagination.total" @current-change="handleTxPageChange" />
        </div>
      </el-tab-pane>

      <el-tab-pane label="支付回调">
        <div class="filter-card">
          <el-select v-model="callbackQuery.provider" clearable placeholder="渠道">
            <el-option label="微信" value="wechat" />
            <el-option label="支付宝" value="alipay" />
          </el-select>
          <el-select v-model="callbackQuery.status" clearable placeholder="状态">
            <el-option label="已接收" value="received" />
            <el-option label="已处理" value="processed" />
            <el-option label="已忽略" value="ignored" />
          </el-select>
          <el-button type="primary" :loading="callbackLoading" @click="searchCallbacks">查询</el-button>
        </div>
        <el-alert title="安全提示：后端不会返回 notify_body，前端不得展示明文回调报文。" type="warning" :closable="false" show-icon class="safe-alert" />
        <el-table :data="callbacks" v-loading="callbackLoading" border>
          <el-table-column prop="id" label="ID" width="90" />
          <el-table-column prop="order_id" label="订单 ID" width="110" />
          <el-table-column prop="provider" label="渠道" width="110" />
          <el-table-column prop="provider_trade_no" label="第三方流水号" min-width="200" />
          <el-table-column prop="status" label="状态" width="110" />
          <el-table-column label="处理时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.processed_at) }}</template></el-table-column>
          <el-table-column label="创建时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination background layout="prev, pager, next, total" :current-page="callbackPagination.page" :page-size="callbackPagination.page_size" :total="callbackPagination.total" @current-change="handleCallbackPageChange" />
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="freezeDialogVisible" :title="freezeForm.action === 'freeze' ? '冻结钱包金额' : '解冻钱包金额'" width="480px">
      <el-form label-width="90px">
        <el-form-item label="操作">
          <el-select v-model="freezeForm.action" style="width: 100%">
            <el-option label="冻结" value="freeze" />
            <el-option label="解冻" value="unfreeze" />
          </el-select>
        </el-form-item>
        <el-form-item label="金额" required><el-input v-model="freezeForm.amount" placeholder="例如 50.00" /></el-form-item>
        <el-form-item label="原因"><el-input v-model="freezeForm.reason" type="textarea" :rows="3" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="freezeDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="freezing" @click="submitFreeze">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.wallet-page { padding: 0; }
.page-header { margin-bottom: 16px; }
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
.filter-card .el-select { width: 130px; }
.wallet-card {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  background: var(--mc-surface);
}
.wallet-card div {
  display: grid;
  gap: 6px;
}
.wallet-card span {
  color: var(--mc-text-muted);
  font-size: 12px;
}
.wallet-card strong {
  color: var(--mc-text);
}
.wallet-actions {
  display: flex !important;
  align-items: end;
  grid-template-columns: none !important;
}
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
.safe-alert { margin-bottom: 12px; }
@media (max-width: 900px) {
  .filter-card,
  .wallet-card {
    grid-template-columns: 1fr;
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
