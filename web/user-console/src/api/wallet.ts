import http from './http'
import type { RechargeOrderResult, Wallet, WalletTransaction } from '@/types/wallet'
import type { PageResult } from '@/types/api'

export function getMyWallet() {
  return http.get<unknown, Wallet>('/wallet')
}

export function listMyTransactions(params: {
  type?: string
  direction?: string
  created_from?: string
  created_to?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<WalletTransaction>>('/wallet/transactions', { params })
}

export function createRechargeOrder(body: {
  amount: string
  payment_method: 'wechat' | 'alipay'
  return_url?: string
}) {
  return http.post<unknown, RechargeOrderResult>('/recharge/orders', body)
}
