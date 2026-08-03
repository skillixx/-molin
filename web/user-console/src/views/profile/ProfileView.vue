<script setup lang="ts">
/**
 * 个人信息页（需登录）
 * 包含 4 个模块：
 *   1. 基本信息展示（只读）
 *   2. 修改用户名
 *   3. 修改手机号（两步 OTP）
 *   4. 修改邮箱（两步 OTP）
 *   5. 修改密码
 */
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import {
  updateUsername,
  updatePhone,
  updateEmail,
  changePassword,
  sendBindPhoneCode,
  sendBindEmailCode,
} from '@/api/auth'
import { maskPhone } from '@/utils/privacy'
import { getSmsSendErrorMessage } from '@/utils/sms'

const authStore = useAuthStore()

// D-93 登录响应只含脱敏摘要；进入个人中心时补拉完整资料，确保用户名和创建时间真实可用。
onMounted(async () => {
  try {
    await authStore.fetchMe()
  } catch (error: unknown) {
    ElMessage.error(getErrorMessage(error, '个人资料加载失败，请稍后重试'))
  }
})

// =================== 实名状态映射 ===================

// 实名状态映射，type 值对齐 el-tag 的 type 属性
const realNameMap: Record<string, { label: string; type: 'success' | 'warning' | 'info' | 'danger' }> = {
  unverified: { label: '未认证', type: 'info' },
  pending:    { label: '审核中', type: 'warning' },
  verified:   { label: '已认证', type: 'success' },
  rejected:   { label: '已拒绝', type: 'danger' },
}

const realNameTag = computed(() => {
  const status = authStore.currentUser?.real_name_status ?? 'unverified'
  return realNameMap[status] ?? realNameMap['unverified']
})

// =================== 模块一：修改用户名 ===================

const usernameFormRef = ref<FormInstance>()
const usernameForm = reactive({ username: '' })
const usernameSubmitting = ref(false)

const usernameRules: FormRules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    {
      pattern: /^[a-zA-Z0-9_]{2,32}$/,
      message: '用户名为2-32位字母、数字或下划线',
      trigger: 'blur',
    },
  ],
}

async function handleUpdateUsername() {
  const valid = await usernameFormRef.value?.validate().catch(() => false)
  if (!valid) return

  usernameSubmitting.value = true
  try {
    await updateUsername(usernameForm.username)
    ElMessage.success('用户名修改成功')
    usernameForm.username = ''
    await authStore.fetchMe()
  } catch (err: unknown) {
    const status = (err as { response?: { status?: number } })?.response?.status
    if (status === 409) {
      ElMessage.error('用户名已被占用')
    }
    // 其他错误由 http 拦截器统一处理
  } finally {
    usernameSubmitting.value = false
  }
}

// =================== 模块二：修改手机号 ===================

const phoneStep = ref<1 | 2>(1)   // 1=填手机号，2=填验证码
const phoneFormRef = ref<FormInstance>()
const phoneForm = reactive({ phone: '', code: '' })
const phoneCountdown = ref(0)
const phoneSending = ref(false)
const phoneSubmitting = ref(false)
let phoneTimer: ReturnType<typeof setInterval>

const phoneStep1Rules: FormRules = {
  phone: [
    { required: true, message: '请输入新手机号', trigger: 'blur' },
    { pattern: /^1[3-9]\d{9}$/, message: '请输入正确的11位手机号', trigger: 'blur' },
  ],
}
const phoneStep2Rules: FormRules = {
  code: [
    { required: true, message: '请输入验证码', trigger: 'blur' },
    { len: 6, message: '验证码为6位', trigger: 'blur' },
  ],
}

async function sendPhoneVerifyCode() {
  const valid = await (phoneFormRef.value?.validateField('phone') as Promise<boolean>).catch(() => false)
  if (!valid) return
  if (phoneCountdown.value > 0 || phoneSending.value) return

  phoneSending.value = true
  try {
    await sendBindPhoneCode(phoneForm.phone)
    ElMessage.success('验证码已发送至新手机号')
    phoneStep.value = 2
    phoneCountdown.value = 60
    phoneTimer = setInterval(() => {
      phoneCountdown.value--
      if (phoneCountdown.value <= 0) clearInterval(phoneTimer)
    }, 1000)
  } catch (err: unknown) {
    ElMessage.error(getSmsSendErrorMessage(err))
  } finally {
    phoneSending.value = false
  }
}

async function handleUpdatePhone() {
  const valid = await phoneFormRef.value?.validate().catch(() => false)
  if (!valid) return

  phoneSubmitting.value = true
  try {
    await updatePhone({ phone: phoneForm.phone, code: phoneForm.code })
    ElMessage.success('手机号修改成功')
    phoneForm.phone = ''
    phoneForm.code = ''
    phoneStep.value = 1
    clearInterval(phoneTimer)
    phoneCountdown.value = 0
    await authStore.fetchMe()
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 40000) {
      ElMessage.error('验证码错误或已过期')
    }
  } finally {
    phoneSubmitting.value = false
  }
}

function resetPhoneStep() {
  phoneStep.value = 1
  phoneForm.phone = ''
  phoneForm.code = ''
  clearInterval(phoneTimer)
  phoneCountdown.value = 0
}

// =================== 模块三：修改邮箱 ===================

const emailStep = ref<1 | 2>(1)
const emailFormRef = ref<FormInstance>()
const emailForm = reactive({ email: '', code: '' })
const emailCountdown = ref(0)
const emailSending = ref(false)
const emailSubmitting = ref(false)
const emailError = ref('')
let emailTimer: ReturnType<typeof setInterval>

// 将换绑失败原因保留在当前卡片内，便于用户修正后再次提交。
function getErrorMessage(error: unknown, fallback: string) {
  return (error as { response?: { data?: { message?: string } } })?.response?.data?.message || fallback
}

const emailStep1Rules: FormRules = {
  email: [
    { required: true, message: '请输入新邮箱地址', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
}
const emailStep2Rules: FormRules = {
  code: [
    { required: true, message: '请输入验证码', trigger: 'blur' },
    { pattern: /^\d{6}$/, message: '验证码为6位数字', trigger: 'blur' },
  ],
}

async function sendEmailVerifyCode() {
  // 在异步表单校验前先关闭重复入口，避免快速连点产生两次换绑邮件。
  if (emailCountdown.value > 0 || emailSending.value) return

  emailSending.value = true
  try {
    const valid = await (emailFormRef.value?.validateField('email') as Promise<boolean>).catch(() => false)
    if (!valid) return

    emailError.value = ''
    await sendBindEmailCode(emailForm.email)
    ElMessage.success('验证码已发送至新邮箱')
    emailStep.value = 2
    emailCountdown.value = 60
    emailTimer = setInterval(() => {
      emailCountdown.value--
      if (emailCountdown.value <= 0) clearInterval(emailTimer)
    }, 1000)
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    const fallback = code === 42900 ? '发送频率超限，请稍后再试' : '验证码发送失败，请稍后重试'
    emailError.value = getErrorMessage(err, fallback)
    ElMessage.error(emailError.value)
  } finally {
    emailSending.value = false
  }
}

async function handleUpdateEmail() {
  const valid = await emailFormRef.value?.validate().catch(() => false)
  if (!valid) return

  emailError.value = ''
  emailSubmitting.value = true
  try {
    await updateEmail({ email: emailForm.email, code: emailForm.code })
    ElMessage.success('邮箱修改成功')
    emailForm.email = ''
    emailForm.code = ''
    emailStep.value = 1
    clearInterval(emailTimer)
    emailCountdown.value = 0
    await authStore.fetchMe()
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    const fallback = code === 40000 ? '验证码错误或已过期' : '邮箱修改失败，请稍后重试'
    emailError.value = getErrorMessage(err, fallback)
    ElMessage.error(emailError.value)
  } finally {
    emailSubmitting.value = false
  }
}

function resetEmailStep() {
  emailError.value = ''
  emailStep.value = 1
  emailForm.email = ''
  emailForm.code = ''
  clearInterval(emailTimer)
  emailCountdown.value = 0
}

// =================== 模块四：修改密码 ===================

const passwordFormRef = ref<FormInstance>()
const passwordForm = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})
const passwordSubmitting = ref(false)

// D-94：改密入口统一执行 6-72 位边界，避免前端规则与后端契约不一致。
const passwordRules: FormRules = {
  old_password: [
    { required: true, message: '请输入旧密码', trigger: 'blur' },
    { min: 6, max: 72, message: '密码长度须为6-72位', trigger: 'blur' },
  ],
  new_password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 6, max: 72, message: '密码长度须为6-72位', trigger: 'blur' },
  ],
  confirm_password: [
    { required: true, message: '请确认新密码', trigger: 'blur' },
    {
      validator: (_rule: unknown, val: string, cb: (err?: Error) => void) => {
        if (val !== passwordForm.new_password) {
          cb(new Error('两次密码不一致'))
        } else {
          cb()
        }
      },
      trigger: 'blur',
    },
  ],
}

async function handleChangePassword() {
  const valid = await passwordFormRef.value?.validate().catch(() => false)
  if (!valid) return

  try {
    await ElMessageBox.confirm(
      '修改密码后，请使用新密码进行后续登录。确认要修改密码吗？',
      '确认修改密码',
      {
        confirmButtonText: '确认修改',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
  } catch {
    return
  }

  passwordSubmitting.value = true
  try {
    await changePassword({
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password,
    })
    ElMessage.success('密码修改成功')
    // 清空表单
    passwordForm.old_password = ''
    passwordForm.new_password = ''
    passwordForm.confirm_password = ''
    passwordFormRef.value?.clearValidate()
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 40001) {
      ElMessage.error('旧密码错误')
    }
  } finally {
    passwordSubmitting.value = false
  }
}

// 页面销毁时清除计时器
onUnmounted(() => {
  clearInterval(phoneTimer)
  clearInterval(emailTimer)
})
</script>

<template>
  <div class="profile-page page-bg">
    <div class="profile-container">
      <!-- 页面标题 -->
      <div class="page-header">
        <div>
          <p class="page-kicker">Account Center</p>
          <h2 class="page-title">个人信息</h2>
          <p class="page-desc">管理账号资料、安全绑定和登录密码。</p>
        </div>
        <el-tag :type="realNameTag.type" effect="dark">
          {{ realNameTag.label }}
        </el-tag>
      </div>

      <section class="profile-hero glass-card">
        <div class="hero-main">
          <div class="avatar-orb">
            <el-icon><user-filled /></el-icon>
          </div>
          <div class="hero-copy">
            <p class="hero-label">当前账号</p>
            <h3>{{ authStore.currentUser?.username || authStore.currentUser?.email || authStore.currentUser?.phone || '墨灵用户' }}</h3>
            <p>用户 ID：<span>{{ authStore.currentUser?.id ?? '—' }}</span></p>
          </div>
        </div>
        <div class="hero-stats">
          <div class="hero-stat">
            <span class="stat-label">手机号</span>
            <strong :class="{ muted: !authStore.currentUser?.phone }">
              {{ authStore.currentUser?.phone ?? '未绑定' }}
            </strong>
          </div>
          <div class="hero-stat">
            <span class="stat-label">邮箱</span>
            <strong :class="{ muted: !authStore.currentUser?.email }">
              {{ authStore.currentUser?.email ?? '未绑定' }}
            </strong>
          </div>
          <div class="hero-stat">
            <span class="stat-label">注册时间</span>
            <strong class="muted">
              {{ authStore.currentUser?.created_at
                  ? new Date(authStore.currentUser.created_at).toLocaleDateString('zh-CN')
                  : '—' }}
            </strong>
          </div>
        </div>
      </section>

      <div class="profile-content">
        <!-- 模块一：基本信息展示 -->
        <div class="profile-card glass-card info-card">
          <div class="card-header">
            <span class="icon-box">
              <el-icon><user /></el-icon>
            </span>
            <div>
              <span class="card-title">基本信息</span>
              <p class="card-subtitle">账号当前展示资料</p>
            </div>
          </div>
          <div class="info-grid">
            <div class="info-item">
              <span class="info-label">用户 ID</span>
              <span class="info-value accent">{{ authStore.currentUser?.id ?? '—' }}</span>
            </div>
            <div class="info-item">
              <span class="info-label">用户名</span>
              <span class="info-value" :class="{ muted: !authStore.currentUser?.username }">
                {{ authStore.currentUser?.username ?? '未设置' }}
              </span>
            </div>
            <div class="info-item">
              <span class="info-label">手机号</span>
              <span class="info-value" :class="{ muted: !authStore.currentUser?.phone }">
                {{ authStore.currentUser?.phone ?? '未绑定' }}
              </span>
            </div>
            <div class="info-item">
              <span class="info-label">邮箱</span>
              <span class="info-value" :class="{ muted: !authStore.currentUser?.email }">
                {{ authStore.currentUser?.email ?? '未绑定' }}
              </span>
            </div>
            <div class="info-item">
              <span class="info-label">实名认证</span>
              <el-tag
                :type="realNameTag.type"
                size="small"
                effect="dark"
              >
                {{ realNameTag.label }}
              </el-tag>
            </div>
            <div class="info-item">
              <span class="info-label">注册时间</span>
              <span class="info-value muted">
                {{ authStore.currentUser?.created_at
                    ? new Date(authStore.currentUser.created_at).toLocaleDateString('zh-CN')
                    : '—' }}
              </span>
            </div>
          </div>
        </div>

        <!-- 模块二：修改用户名 -->
        <div class="profile-card glass-card">
          <div class="card-header">
            <span class="icon-box">
              <el-icon><edit /></el-icon>
            </span>
            <div>
              <span class="card-title">修改用户名</span>
              <p class="card-subtitle">2-32位字母、数字或下划线</p>
            </div>
          </div>
          <el-form
            ref="usernameFormRef"
            :model="usernameForm"
            :rules="usernameRules"
            label-position="top"
            class="profile-form"
          >
            <el-form-item label="新用户名" prop="username">
              <div class="inline-row">
                <el-input
                  v-model="usernameForm.username"
                  placeholder="请输入新用户名"
                  maxlength="32"
                  :prefix-icon="'User'"
                  size="large"
                />
                <el-button
                  type="primary"
                  :loading="usernameSubmitting"
                  class="inline-btn"
                  @click="handleUpdateUsername"
                >
                  保存
                </el-button>
              </div>
            </el-form-item>
          </el-form>
        </div>

        <!-- 模块三：修改手机号 -->
        <div class="profile-card glass-card">
          <div class="card-header">
            <span class="icon-box">
              <el-icon><phone /></el-icon>
            </span>
            <div>
              <span class="card-title">修改手机号</span>
              <p class="card-subtitle">
                当前绑定：<span :class="{ accent: authStore.currentUser?.phone, muted: !authStore.currentUser?.phone }">{{ authStore.currentUser?.phone || '未绑定' }}</span>
              </p>
            </div>
          </div>

          <el-form
            ref="phoneFormRef"
            :model="phoneForm"
            :rules="phoneStep === 1 ? phoneStep1Rules : phoneStep2Rules"
            label-position="top"
            class="profile-form"
          >
            <!-- Step 1：输入新手机号 -->
            <template v-if="phoneStep === 1">
              <el-form-item label="新手机号" prop="phone">
                <div class="inline-row">
                  <el-input
                    v-model="phoneForm.phone"
                    placeholder="请输入新手机号"
                    maxlength="11"
                    size="large"
                  />
                  <el-button
                    type="primary"
                    :loading="phoneSending"
                    class="inline-btn"
                    @click="sendPhoneVerifyCode"
                  >
                    发送验证码
                  </el-button>
                </div>
              </el-form-item>
            </template>

            <!-- Step 2：输入验证码 -->
            <template v-else>
              <p class="step-hint">
                验证码已发送至 <span class="accent">{{ maskPhone(phoneForm.phone) }}</span>
                <el-link class="change-link" @click="resetPhoneStep">更换号码</el-link>
              </p>
              <el-form-item label="验证码" prop="code">
                <div class="code-row">
                  <el-input
                    v-model="phoneForm.code"
                    placeholder="请输入6位验证码"
                    maxlength="6"
                    size="large"
                  />
                  <button
                    class="code-btn"
                    :disabled="phoneCountdown > 0 || phoneSending"
                    @click.prevent="sendPhoneVerifyCode"
                  >
                    {{ phoneCountdown > 0 ? `${phoneCountdown}s 后重发` : '重新发送' }}
                  </button>
                </div>
              </el-form-item>
              <el-form-item>
                <el-button
                  type="primary"
                  :loading="phoneSubmitting"
                  class="submit-btn"
                  @click="handleUpdatePhone"
                >
                  确认修改手机号
                </el-button>
              </el-form-item>
            </template>
          </el-form>
        </div>

        <!-- 模块四：修改邮箱 -->
        <div class="profile-card glass-card">
          <div class="card-header">
            <span class="icon-box">
              <el-icon><message /></el-icon>
            </span>
            <div>
              <span class="card-title">修改邮箱</span>
              <p class="card-subtitle">
                当前绑定：<span :class="{ accent: authStore.currentUser?.email, muted: !authStore.currentUser?.email }">{{ authStore.currentUser?.email || '未绑定' }}</span>
              </p>
            </div>
          </div>

          <el-form
            ref="emailFormRef"
            :model="emailForm"
            :rules="emailStep === 1 ? emailStep1Rules : emailStep2Rules"
            label-position="top"
            class="profile-form"
          >
            <p v-if="emailError" class="form-error" role="alert">
              {{ emailError }}
            </p>

            <!-- Step 1：输入新邮箱 -->
            <template v-if="emailStep === 1">
              <el-form-item label="新邮箱地址" prop="email">
                <div class="inline-row">
                  <el-input
                    v-model="emailForm.email"
                    placeholder="请输入新邮箱地址"
                    type="email"
                    size="large"
                  />
                  <el-button
                    type="primary"
                    :loading="emailSending"
                    class="inline-btn"
                    @click="sendEmailVerifyCode"
                  >
                    发送验证码
                  </el-button>
                </div>
              </el-form-item>
            </template>

            <!-- Step 2：输入验证码 -->
            <template v-else>
              <p class="step-hint">
                验证码已发送至 <span class="accent">{{ emailForm.email }}</span>
                <el-link class="change-link" @click="resetEmailStep">更换邮箱</el-link>
              </p>
              <el-form-item label="验证码" prop="code">
                <div class="code-row">
                  <el-input
                    v-model="emailForm.code"
                    placeholder="请输入6位验证码"
                    maxlength="6"
                    size="large"
                  />
                  <button
                    class="code-btn"
                    :disabled="emailCountdown > 0 || emailSending"
                    @click.prevent="sendEmailVerifyCode"
                  >
                    {{ emailCountdown > 0 ? `${emailCountdown}s 后重发` : '重新发送' }}
                  </button>
                </div>
              </el-form-item>
              <el-form-item>
                <el-button
                  type="primary"
                  :loading="emailSubmitting"
                  class="submit-btn"
                  @click="handleUpdateEmail"
                >
                  确认修改邮箱
                </el-button>
              </el-form-item>
            </template>
          </el-form>
        </div>

        <!-- 模块五：修改密码 -->
        <div class="profile-card glass-card password-card">
          <div class="card-header">
            <span class="icon-box">
              <el-icon><lock /></el-icon>
            </span>
            <div>
              <span class="card-title">修改密码</span>
              <p class="card-subtitle">密码长度为6-72位，建议组合使用字母、数字和符号</p>
            </div>
          </div>
          <el-form
            ref="passwordFormRef"
            :model="passwordForm"
            :rules="passwordRules"
            label-position="top"
            class="profile-form password-form"
          >
            <el-form-item label="当前密码" prop="old_password">
              <el-input
                v-model="passwordForm.old_password"
                type="password"
                placeholder="请输入当前密码"
                show-password
                autocomplete="current-password"
                maxlength="72"
                size="large"
              />
            </el-form-item>
            <el-form-item label="新密码" prop="new_password">
              <el-input
                v-model="passwordForm.new_password"
                type="password"
                placeholder="6-72位"
                show-password
                autocomplete="new-password"
                maxlength="72"
                size="large"
              />
            </el-form-item>
            <el-form-item label="确认新密码" prop="confirm_password">
              <el-input
                v-model="passwordForm.confirm_password"
                type="password"
                placeholder="再次输入新密码"
                show-password
                autocomplete="new-password"
                maxlength="72"
                size="large"
              />
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                :loading="passwordSubmitting"
                class="submit-btn"
                @click="handleChangePassword"
              >
                修改密码
              </el-button>
            </el-form-item>
          </el-form>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.profile-page {
  min-height: 100%;
  padding: 34px 24px 56px;
  position: relative;
  overflow: hidden;
}

.profile-page::before {
  content: "";
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 12% 12%, rgba(6, 182, 212, 0.16), transparent 24%),
    radial-gradient(circle at 88% 8%, rgba(139, 92, 246, 0.14), transparent 26%);
  pointer-events: none;
}

.profile-container {
  position: relative;
  z-index: 1;
  max-width: 1120px;
  margin: 0 auto;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 18px;
  margin-bottom: 24px;
}

.page-kicker {
  color: var(--color-accent);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  margin-bottom: 8px;
}

.page-title {
  font-size: 30px;
  line-height: 1.2;
  font-weight: 800;
  color: var(--color-text);
  margin-bottom: 10px;
}

.page-desc {
  font-size: 14px;
  color: var(--color-text-muted);
  line-height: 1.7;
}

.profile-hero {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 28px;
  align-items: center;
  padding: 30px;
  margin-bottom: 22px;
  background:
    linear-gradient(145deg, rgba(99, 102, 241, 0.14), rgba(6, 182, 212, 0.06)),
    rgba(11, 16, 32, 0.74);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.28);
  overflow: hidden;
  position: relative;
}

.profile-hero::after {
  content: "";
  position: absolute;
  right: -120px;
  bottom: -150px;
  width: 340px;
  height: 340px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(6, 182, 212, 0.16), transparent 66%);
  pointer-events: none;
}

.hero-main,
.hero-stats {
  position: relative;
  z-index: 1;
}

.hero-main {
  display: flex;
  align-items: center;
  gap: 18px;
  min-width: 0;
}

.avatar-orb {
  width: 72px;
  height: 72px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 20px;
  color: var(--color-accent);
  font-size: 34px;
  background: rgba(6, 182, 212, 0.12);
  border: 1px solid rgba(6, 182, 212, 0.28);
  box-shadow: 0 18px 42px rgba(6, 182, 212, 0.14);
}

.hero-copy {
  min-width: 0;
}

.hero-label {
  color: var(--color-text-muted);
  font-size: 13px;
  margin-bottom: 6px;
}

.hero-copy h3 {
  color: var(--color-text);
  font-size: 26px;
  line-height: 1.25;
  font-weight: 800;
  margin-bottom: 8px;
  word-break: break-word;
}

.hero-copy p {
  color: var(--color-text-muted);
  font-size: 13px;
}

.hero-copy p span {
  color: var(--color-accent);
}

.hero-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(130px, 1fr));
  gap: 12px;
}

.hero-stat {
  min-height: 72px;
  padding: 14px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 14px;
  background: rgba(10, 15, 30, 0.46);
}

.stat-label {
  display: block;
  color: var(--color-text-muted);
  font-size: 12px;
  margin-bottom: 8px;
}

.hero-stat strong {
  display: block;
  max-width: 190px;
  color: var(--color-text);
  font-size: 13px;
  font-weight: 700;
  word-break: break-word;
}

.profile-content {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 20px;
}

.profile-card {
  padding: 24px;
  background: rgba(11, 16, 32, 0.74);
  box-shadow: 0 18px 54px rgba(0, 0, 0, 0.22);
}

.info-card,
.password-card {
  grid-column: 1 / -1;
}

.card-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 18px;
}

.icon-box {
  width: 42px;
  height: 42px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-accent);
  border-radius: 12px;
  background: rgba(6, 182, 212, 0.1);
  border: 1px solid rgba(6, 182, 212, 0.22);
  font-size: 19px;
}

.card-title {
  display: block;
  font-size: 16px;
  font-weight: 700;
  color: var(--color-text);
  margin-bottom: 4px;
}

.card-subtitle {
  font-size: 13px;
  color: var(--color-text-muted);
  line-height: 1.5;
}

.info-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.info-item {
  min-height: 74px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 7px;
  padding: 14px;
  border: 1px solid var(--color-border);
  border-radius: 12px;
  background: rgba(99, 102, 241, 0.055);
  min-width: 0;
}

.info-label {
  font-size: 12px;
  color: var(--color-text-muted);
}

.info-value {
  min-width: 0;
  font-size: 14px;
  color: var(--color-text);
  font-weight: 600;
  word-break: break-word;
}

.info-value.accent {
  color: var(--color-accent);
}

.info-value.muted,
.muted {
  color: var(--color-text-muted) !important;
  font-weight: 500;
}

.accent {
  color: var(--color-accent);
}

.profile-form {
  margin-top: 4px;
}

.form-error {
  margin: 0 0 14px;
  padding: 10px 12px;
  border: 1px solid rgba(248, 113, 113, 0.36);
  border-radius: 8px;
  color: #fecaca;
  background: rgba(127, 29, 29, 0.18);
  font-size: 13px;
  line-height: 1.5;
}

.password-form {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  column-gap: 14px;
  align-items: start;
}

.password-form .el-form-item:last-child {
  grid-column: 1 / -1;
}

.inline-row {
  display: flex;
  gap: 10px;
  width: 100%;
}

.inline-row .el-input {
  flex: 1;
}

.inline-btn {
  flex-shrink: 0;
  min-width: 112px;
  min-height: 44px;
  background: var(--gradient-primary) !important;
  border: none !important;
  color: #fff !important;
  font-size: 13px;
  transition: filter 0.2s, box-shadow 0.2s;
}

.inline-btn:hover {
  filter: brightness(1.15);
  box-shadow: var(--shadow-glow);
}

.submit-btn {
  width: 100%;
  height: 44px;
  background: var(--gradient-primary) !important;
  border: none !important;
  font-size: 14px;
}

.step-hint {
  font-size: 13px;
  color: var(--color-text-muted);
  margin-bottom: 14px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 11px 13px;
  border: 1px solid rgba(6, 182, 212, 0.18);
  border-radius: 12px;
  background: rgba(6, 182, 212, 0.06);
}

.change-link {
  font-size: 12px;
  color: var(--color-primary) !important;
  cursor: pointer;
}

.code-row {
  display: flex;
  gap: 10px;
  width: 100%;
}

.code-row .el-input {
  flex: 1;
}

.code-btn {
  flex-shrink: 0;
  min-width: 106px;
  padding: 0 12px;
  min-height: 44px;
  background: rgba(99, 102, 241, 0.12);
  border: 1px solid var(--color-border);
  color: var(--color-primary);
  border-radius: 8px;
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.2s, border-color 0.2s;
}

.code-btn:hover:not(:disabled) {
  background: rgba(99, 102, 241, 0.2);
  border-color: var(--color-primary);
}

.code-btn:disabled {
  color: var(--color-text-disabled);
  border-color: rgba(99, 102, 241, 0.1);
  cursor: not-allowed;
}

:deep(.el-input__wrapper) {
  min-height: 44px;
  border-radius: 9px;
}

@media (max-width: 980px) {
  .profile-hero {
    grid-template-columns: 1fr;
  }

  .hero-stats,
  .info-grid,
  .password-form {
    grid-template-columns: 1fr;
  }

  .profile-content {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .profile-page {
    padding: 24px 14px 40px;
  }

  .page-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .page-title {
    font-size: 26px;
  }

  .profile-hero,
  .profile-card {
    padding: 22px;
  }

  .hero-main {
    align-items: flex-start;
  }

  .inline-row,
  .code-row {
    flex-direction: column;
  }

  .inline-btn,
  .code-btn {
    width: 100%;
  }
}
</style>
