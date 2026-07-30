export interface EmailTemplateEligibilitySnapshot {
  id: number
  provider_status: string
  local_enabled: boolean
  missing: boolean
  variables_complete: boolean
}

/**
 * 返回场景绑定不可保存的原因；空字符串表示当前模板快照满足全部前置条件。
 * 候选列表异常时必须失败关闭，不能继续使用可能已过期的旧快照提交配置。
 */
export function emailSceneBindingBlockReason(
  templateId: number | null,
  templates: readonly EmailTemplateEligibilitySnapshot[],
  candidatesError: string,
) {
  if (candidatesError) return '合规模板加载失败，请重新加载后再保存'
  if (templateId == null) return '请选择合规模板'

  const template = templates.find(item => item.id === templateId)
  if (!template) return '所选模板当前不可用，请重新选择'
  if (template.provider_status !== 'approved') return '邮件模板尚未审核通过'
  if (!template.local_enabled) return '邮件模板已停用'
  if (template.missing) return '邮件模板在供应商侧不存在'
  if (!template.variables_complete) return '邮件模板变量不完整'
  return ''
}
