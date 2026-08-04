import type { SmsScene, SmsSceneBinding, SmsTemplate } from '@/types/sms'

type SmsTemplatePolicyView = Pick<
  SmsTemplate,
  'id' | 'provider_audit_status' | 'template_type' | 'variables' | 'local_enabled' | 'bound_scenes'
>

type SmsScenePolicyView = Pick<SmsSceneBinding, 'scene' | 'template_id' | 'enabled'>

/**
 * 返回模板不能用于绑定或测试的原因；空字符串表示满足前端可判断的必要条件。
 * 后端仍是最终安全边界，前端策略只用于给管理员提前提供可理解的阻断提示。
 */
export function smsTemplateBlockReason(template: SmsTemplatePolicyView): string {
  if (template.provider_audit_status !== 'approved') return '模板尚未通过阿里云审核'
  if (template.template_type !== 'verification') return '模板不是验证码类型'
  if (template.variables.length !== 1 || template.variables[0] !== 'code') return '模板变量必须且只能包含 code'
  if (!template.local_enabled) return '模板本地已停用'
  return ''
}

/**
 * 场景绑定必须使用合规模板，且一个启用模板不能同时占用多个启用场景。
 * 候选快照加载失败时保持失败关闭，避免管理员基于陈旧数据覆盖配置。
 */
export function smsSceneBindingBlockReason(
  templateId: number,
  targetScene: SmsScene,
  templates: SmsTemplatePolicyView[],
  scenes: SmsScenePolicyView[],
  templatesError: string,
): string {
  if (templatesError) return '候选模板加载失败，请刷新后再配置'
  const template = templates.find(item => item.id === templateId)
  if (!template) return '请选择当前列表中的短信模板'
  const templateReason = smsTemplateBlockReason(template)
  if (templateReason) return templateReason
  const occupied = scenes.find(item => item.enabled && item.template_id === templateId && item.scene !== targetScene)
  if (occupied) return '该模板已绑定其他启用场景，请选择独立模板'
  return ''
}

/** 完整手机号只在当前表单内校验，不进行格式化或持久化。 */
export function validateTestPhone(phone: string): string {
  if (!/^1[3-9]\d{9}$/.test(phone)) return '请输入单个有效的中国大陆手机号码'
  return ''
}
