import { ElMessage } from 'element-plus'
import { launchApp } from '@/api/app'

type ApiError = {
  response?: {
    data?: {
      code?: number
    }
  }
}

export async function openAppById(appId: number, entitlementId?: number) {
  try {
    const payload = entitlementId ? { entitlement_id: entitlementId } : undefined
    const { access_url, launch_ticket } = await launchApp(appId, payload)
    const separator = access_url.includes('?') ? '&' : '?'
    window.open(
      `${access_url}${separator}ticket=${encodeURIComponent(launch_ticket)}`,
      '_blank',
      'noopener,noreferrer',
    )
  } catch (err) {
    const code = (err as ApiError).response?.data?.code
    if (code === 40003) {
      ElMessage.warning('请先购买或开通')
      return
    }
    if (code === 40400) {
      ElMessage.warning('该应用暂未开放访问入口')
      return
    }
    ElMessage.error('进入应用失败，请稍后重试')
  }
}
