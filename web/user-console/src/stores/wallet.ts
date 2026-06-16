import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getMyWallet } from '@/api/wallet'
import type { Wallet } from '@/types/wallet'

export const useWalletStore = defineStore('wallet', () => {
  const wallet = ref<Wallet | null>(null)
  const loading = ref(false)

  async function fetchBalance() {
    loading.value = true
    try {
      wallet.value = await getMyWallet()
    } catch {
      wallet.value = null
    } finally {
      loading.value = false
    }
  }

  function formatBalance(value?: string): string {
    return value ?? wallet.value?.balance_amount ?? '0'
  }

  return { wallet, loading, fetchBalance, formatBalance }
})
