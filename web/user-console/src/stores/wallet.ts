/**
 * 钱包状态管理（Week 2 实现充值/流水接口后完善）
 * 当前只保留 balance 状态和 fetchBalance 框架
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useWalletStore = defineStore('wallet', () => {
  // 钱包余额（单位：分）
  const balance = ref<number>(0)
  // 是否正在加载余额
  const loading = ref(false)

  /**
   * 获取钱包余额
   * TODO: Week 2 对接 GET /api/wallet/balance
   */
  async function fetchBalance() {
    loading.value = true
    try {
      // TODO: Week 2 接入
      // const data = await http.get<unknown, { balance: number }>('/wallet/balance')
      // balance.value = data.balance
    } finally {
      loading.value = false
    }
  }

  /**
   * 余额格式化（分 → 元，精确到两位小数）
   */
  function formatBalance(fen?: number): string {
    const val = fen ?? balance.value
    return (val / 100).toFixed(2)
  }

  return { balance, loading, fetchBalance, formatBalance }
})
