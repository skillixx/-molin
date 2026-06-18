import http from './http'
import type { Order } from '@/types/order-admin'
import type { PageResult } from '@/types/api'

export function listAdminOrders(params: {
  user_id?: number
  status?: string
  order_type?: string
  created_from?: string
  created_to?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Order>>('/admin/orders', { params })
}

export function getAdminOrder(id: number) {
  return http.get<unknown, Order>(`/admin/orders/${id}`)
}
