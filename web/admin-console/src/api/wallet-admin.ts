import http from './http'
import type { PaymentCallback, Wallet, WalletTransaction } from '@/types/wallet-admin'
import type { PageResult } from '@/types/api'

export function getUserWallet(userId: number) {
  return http.get<unknown, Wallet>(`/admin/users/${userId}/wallet`)
}

export function listAllTransactions(params: {
  user_id?: number
  type?: string
  direction?: string
  created_from?: string
  created_to?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<WalletTransaction>>('/admin/wallet-transactions', { params })
}

export function freezeUserWallet(
  userId: number,
  data: { action: 'freeze' | 'unfreeze'; amount: string; reason?: string }
) {
  return http.patch<unknown, { message: string }>(`/admin/users/${userId}/wallet/freeze`, data)
}

export function listPaymentCallbacks(params: {
  provider?: string
  status?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<PaymentCallback>>('/admin/payment-callbacks', { params })
}
