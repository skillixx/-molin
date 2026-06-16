import http from './http'
import type { Order, PayOrderResult } from '@/types/order'
import type { PageResult } from '@/types/api'

export function listMyOrders(params: {
  status?: string
  order_type?: string
  created_from?: string
  created_to?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Order>>('/orders', { params })
}

export function getOrder(id: number) {
  return http.get<unknown, Order>(`/orders/${id}`)
}

export function payOrder(id: number, idempotencyKey: string) {
  return http.post<unknown, PayOrderResult>(
    `/orders/${id}/pay`,
    { pay_method: 'wallet' },
    { headers: { 'Idempotency-Key': idempotencyKey } },
  )
}

export function cancelOrder(id: number, reason?: string) {
  return http.post<unknown, { cancelled: boolean }>(`/orders/${id}/cancel`, { reason })
}
