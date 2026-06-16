import http from './http'
import type { ConsumptionRecord } from '@/types/consumption'
import type { PageResult } from '@/types/api'

export function listMyConsumptionRecords(params: {
  product_id?: number
  usage_type?: string
  created_from?: string
  created_to?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<ConsumptionRecord>>('/product-consumption-records', { params })
}
