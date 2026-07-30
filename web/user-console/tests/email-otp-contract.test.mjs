import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = path => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8')

const api = read('src/api/auth.ts')
const authTypes = read('src/types/auth.ts')
const authStore = read('src/stores/auth.ts')
const register = read('src/views/auth/RegisterView.vue')
const login = read('src/views/auth/LoginView.vue')
const reset = read('src/views/auth/ResetPasswordView.vue')
const profile = read('src/views/profile/ProfileView.vue')

test('公开邮箱发码仅接受注册、登录和找回密码三个固定场景', () => {
  assert.match(api, /scene:\s*'register'\s*\|\s*'login'\s*\|\s*'reset_password'/)
  assert.match(api, /'\/auth\/verification-codes\/email',\s*\{ email, scene \}/)
  assert.doesNotMatch(api, /sendEmailCode[\s\S]{0,220}'bind_email'/)
})

test('邮箱验证码登录保留密码入口并使用严格登录接口', () => {
  assert.match(login, /emailLoginMode\s*=\s*ref<'password'\s*\|\s*'code'>\('password'\)/)
  assert.match(login, /await sendEmailCode\(emailForm\.email, 'login'\)/)
  assert.match(login, /await authStore\.loginWithEmailCode\(emailForm\.email, emailForm\.code\)/)
  assert.match(api, /'\/auth\/login\/email\/code', body/)
  assert.match(login, /emailCountdown\.value > 0 \|\| sendingCode\.value \|\| submitting\.value/)
})

test('找回密码固定使用 reset_password 且密码长度与 D-94 一致', () => {
  assert.equal(reset.match(/sendEmailCode\(targetValue\.value, 'reset_password'\)/g)?.length, 2)
  assert.match(reset, /\{ min: 6, max: 72, message: '密码长度须为6-72位'/)
  assert.match(reset, /pattern: \/\^\\d\{6\}\$\//)
  assert.equal(reset.match(/maxlength="72"/g)?.length, 2)
  assert.match(api, /'\/auth\/password\/reset', params/)
})

test('换绑邮箱必须走认证态专用接口且场景由服务端固定', () => {
  assert.match(api, /sendBindEmailCode[\s\S]{0,180}'\/me\/verification-codes\/email', \{ email \}/)
  assert.match(profile, /await sendBindEmailCode\(emailForm\.email\)/)
  assert.match(api, /'\/me\/email', params/)
  assert.doesNotMatch(profile, /sendEmailCode\(emailForm\.email,\s*'bind_email'/)
})

test('发码交互防止快速重复请求并兼顾移动端触控区', () => {
  assert.match(
    reset,
    /if \(sendingCode\.value\) return[\s\S]{0,100}sendingCode\.value = true[\s\S]{0,160}step1FormRef\.value\?\.validate/,
  )
  assert.match(profile, /if \(emailCountdown\.value > 0 \|\| emailSending\.value\) return[\s\S]{0,180}validateField\('email'\)/)
  assert.match(login, /@media \(max-width: 520px\)/)
  assert.match(reset, /@media \(max-width: 520px\)/)
  assert.match(profile, /@media \(max-width: 640px\)/)
  assert.equal((profile.match(/min-height: 44px;/g) ?? []).length >= 2, true)
})

test('换绑邮箱验证码只接受六位数字', () => {
  assert.match(
    profile,
    /emailStep2Rules[\s\S]{0,260}pattern:\s*\/\^\\d\{6\}\$\//,
  )
})

test('个人中心修改密码统一执行六至七十二位边界', () => {
  assert.match(
    profile,
    /passwordRules[\s\S]{0,360}new_password:[\s\S]{0,180}\{ min: 6, max: 72,/,
  )
  assert.equal(profile.match(/maxlength="72"/g)?.length, 3)
})

test('邮箱密码登录执行六至七十二位边界', () => {
  assert.match(
    login,
    /emailRules[\s\S]{0,360}password:[\s\S]{0,150}\{ min: 6, max: 72,/,
  )
  assert.match(
    login,
    /v-model="emailForm\.password"[\s\S]{0,180}maxlength="72"/,
  )
})

test('邮箱发码使用不含明文验证码的稳定响应类型', () => {
  assert.match(
    authTypes,
    /interface VerificationCodeSendResult\s*\{\s*sent: boolean\s*expires_in: number\s*\}/,
  )
  assert.equal(
    api.match(/http\.post<unknown, VerificationCodeSendResult>\('\/(?:auth|me)\/verification-codes\/email'/g)?.length,
    2,
  )
  assert.doesNotMatch(authTypes, /interface VerificationCodeSendResult[\s\S]{0,120}\bcode\??:/)
  assert.doesNotMatch(login, /(?:const|let)\s+\w+\s*=\s*await sendEmailCode/)
  assert.doesNotMatch(profile, /(?:const|let)\s+\w+\s*=\s*await sendBindEmailCode/)
})

test('登录注册刷新直接使用 D-93 必填用户摘要且不重复拉取个人信息', () => {
  assert.match(
    authTypes,
    /interface TokenPair\s*\{[\s\S]{0,180}expires_in: number[\s\S]{0,100}user: LoginUserSummary/,
  )
  assert.match(
    authStore,
    /async function applyLoginResponse[\s\S]{0,260}currentUser\.value = toUser\(data\.user\)[\s\S]{0,120}await fetchPermissions\(\)/,
  )
  assert.doesNotMatch(
    authStore,
    /async function applyLoginResponse[\s\S]{0,300}await fetchMe\(\)/,
  )
})

test('注册找回和换绑邮箱在表单内持续展示失败原因', () => {
  for (const [source, stateName] of [
    [register, 'formError'],
    [reset, 'formError'],
    [profile, 'emailError'],
  ]) {
    assert.match(source, new RegExp(`const ${stateName} = ref\\(''\\)`))
    assert.match(source, new RegExp(`v-if="${stateName}"[\\s\\S]{0,100}role="alert"`))
    assert.match(source, new RegExp(`${stateName}\\.value = getErrorMessage\\(`))
  }
})

test('邮件相关表单的输入与操作控件触控高度至少四十四像素', () => {
  assert.match(register, /\.code-btn\s*\{[\s\S]{0,160}min-height: 44px;/)
  assert.match(register, /:deep\(\.el-input__wrapper\)\s*\{\s*min-height: 44px;/)
  assert.match(login, /\.captcha-code\s*\{[\s\S]{0,180}min-height: 44px;/)
  assert.match(login, /:deep\(\.el-input__wrapper\)\s*\{\s*min-height: 44px;/)
  assert.match(reset, /\.method-tab\s*\{[\s\S]{0,100}min-height: 44px;/)
  assert.match(reset, /\.code-btn\s*\{[\s\S]{0,150}min-height: 44px;/)
  assert.match(reset, /:deep\(\.el-input__wrapper\)\s*\{\s*min-height: 44px;/)
  assert.match(profile, /:deep\(\.el-input__wrapper\)\s*\{\s*min-height: 44px;/)
})

test('进入个人中心时补拉完整用户资料且登录成功链路仍不重复请求', () => {
  assert.match(
    profile,
    /onMounted\(async \(\) => \{[\s\S]{0,220}await authStore\.fetchMe\(\)/,
  )
  assert.doesNotMatch(
    authStore,
    /async function applyLoginResponse[\s\S]{0,300}await fetchMe\(\)/,
  )
})

test('邮箱登录发码在异步校验前占用互斥锁并始终释放', () => {
  assert.match(
    login,
    /async function sendEmailLoginCode\(\)[\s\S]{0,220}sendingCode\.value = true[\s\S]{0,220}validateField\(\['email', 'captcha'\]\)/,
  )
  assert.match(
    login,
    /async function sendEmailLoginCode\(\)[\s\S]{0,700}finally \{\s*sendingCode\.value = false\s*\}/,
  )
})

test('换绑邮箱发码在异步校验前占用互斥锁并始终释放', () => {
  assert.match(
    profile,
    /async function sendEmailVerifyCode\(\)[\s\S]{0,260}emailSending\.value = true[\s\S]{0,220}validateField\('email'\)/,
  )
  assert.match(
    profile,
    /async function sendEmailVerifyCode\(\)[\s\S]{0,900}finally \{\s*emailSending\.value = false\s*\}/,
  )
})
