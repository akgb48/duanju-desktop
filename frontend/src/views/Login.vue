<script setup>
/**
 * 登录页（手机号 + 短信验证码）。
 *
 * 流程：填手机号 → 发验证码（60s 倒计时）→ 填码 → 登录 → 回跳。
 * 二次验证：account.login 返回 { mfa:true } 时切到 MFA 步骤。
 */
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAccountStore } from '../stores/account'
import { useUiStore } from '../stores/ui'
import { IS_MOCK } from '../api/bridge'

const route = useRoute()
const router = useRouter()
const account = useAccountStore()
const ui = useUiStore()

const phone = ref('')
const code = ref('')
const mfaCode = ref('')
const step = ref('phone') // phone | code | mfa
const sending = ref(false)
const submitting = ref(false)
const countdown = ref(0)
const errMsg = ref('')

let timer = null

const redirect = computed(() => String(route.query.redirect || ''))

/** 手机号合法：中国大陆 11 位。 */
const phoneValid = computed(() => /^1[3-9]\d{9}$/.test(phone.value.trim()))
const codeValid = computed(() => /^\d{4,6}$/.test(code.value.trim()))

const canSend = computed(() => phoneValid.value && countdown.value === 0 && !sending.value)
const canSubmit = computed(() => phoneValid.value && codeValid.value && !submitting.value)

function startCountdown(sec = 60) {
  countdown.value = sec
  if (timer) clearInterval(timer)
  timer = setInterval(() => {
    countdown.value -= 1
    if (countdown.value <= 0) {
      clearInterval(timer)
      timer = null
      countdown.value = 0
    }
  }, 1000)
}

async function sendCode() {
  if (!canSend.value) return
  sending.value = true
  errMsg.value = ''
  const r = await account.sendCode(phone.value.trim())
  sending.value = false
  if (!r.ok) {
    errMsg.value = r.error
    return
  }
  ui.toastOk('验证码已发送')
  step.value = 'code'
  startCountdown(60)
}

async function submit() {
  if (step.value === 'mfa') {
    await submitMfa()
    return
  }
  if (!canSubmit.value) return
  submitting.value = true
  errMsg.value = ''
  const r = await account.login(phone.value.trim(), code.value.trim())
  submitting.value = false
  if (r.ok) {
    ui.toastOk('登录成功')
    router.replace(redirect.value || '/')
    return
  }
  if (r.mfa) {
    step.value = 'mfa'
    errMsg.value = '需要二次验证'
    return
  }
  errMsg.value = r.error || '登录失败'
}

async function submitMfa() {
  if (!/^\d{6}$/.test(mfaCode.value.trim())) {
    errMsg.value = '请输入 6 位二次验证码'
    return
  }
  submitting.value = true
  errMsg.value = ''
  const r = await account.login(phone.value.trim(), mfaCode.value.trim())
  submitting.value = false
  if (r.ok) {
    ui.toastOk('登录成功')
    router.replace(redirect.value || '/')
  } else {
    errMsg.value = r.error || '验证失败'
  }
}

/** 只允许数字。 */
function digitsOnly(e) {
  e.target.value = e.target.value.replace(/\D/g, '')
  return e.target.value
}

function goBack() {
  if (step.value === 'mfa') step.value = 'code'
  else if (step.value === 'code') step.value = 'phone'
  else router.back()
}

onMounted(() => {
  account.loadRegions()
  // 已登录直接走（浏览器调试模式下保留登录页，方便预览）
  if (account.loggedIn && !IS_MOCK) router.replace(redirect.value || '/')
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="login-page">
    <div class="lg-card">
      <button class="lg-back" title="返回" @click="goBack">‹</button>

      <div class="lg-brand">
        <span class="lg-logo">红</span>
        <div>
          <h1 class="lg-title">短剧电脑版</h1>
          <p class="lg-sub">登录后同步观看记录与书架</p>
        </div>
      </div>

      <div v-if="errMsg" class="lg-err">{{ errMsg }}</div>

      <!-- 手机号 -->
      <template v-if="step === 'phone'">
        <label class="lg-label">手机号</label>
        <div class="lg-field">
          <span class="lg-prefix">+86</span>
          <input
            v-model="phone"
            class="lg-input"
            type="tel"
            inputmode="numeric"
            maxlength="11"
            placeholder="请输入手机号"
            autofocus
            @input="digitsOnly"
            @keydown.enter="sendCode"
          />
        </div>
        <button class="btn btn-primary btn-lg lg-submit" :disabled="!canSend" @click="sendCode">
          {{ sending ? '发送中…' : '获取验证码' }}
        </button>
      </template>

      <!-- 验证码 -->
      <template v-else-if="step === 'code'">
        <label class="lg-label">验证码</label>
        <p class="lg-hint">已发送至 {{ phone }}</p>
        <div class="lg-field">
          <input
            v-model="code"
            class="lg-input lg-input-code"
            type="tel"
            inputmode="numeric"
            maxlength="6"
            placeholder="6 位验证码"
            autofocus
            @input="digitsOnly"
            @keydown.enter="submit"
          />
          <button class="lg-resend" :disabled="countdown > 0 || sending" @click="sendCode">
            {{ countdown > 0 ? `${countdown}s 后重发` : '重新发送' }}
          </button>
        </div>
        <button class="btn btn-primary btn-lg lg-submit" :disabled="!canSubmit" @click="submit">
          {{ submitting ? '登录中…' : '登录' }}
        </button>
      </template>

      <!-- 二次验证 -->
      <template v-else>
        <label class="lg-label">二次验证</label>
        <p class="lg-hint">请输入身份验证器上的 6 位动态码</p>
        <div class="lg-field">
          <input
            v-model="mfaCode"
            class="lg-input lg-input-code"
            type="tel"
            inputmode="numeric"
            maxlength="6"
            placeholder="6 位动态码"
            autofocus
            @input="digitsOnly"
            @keydown.enter="submitMfa"
          />
        </div>
        <button class="btn btn-primary btn-lg lg-submit" :disabled="submitting" @click="submitMfa">
          {{ submitting ? '验证中…' : '验证并登录' }}
        </button>
      </template>

      <div class="lg-alt">
        <button class="lg-alt-btn" @click="router.push({ name: 'login-qr', query: route.query })">
          扫码登录
        </button>
        <span class="lg-alt-sep">·</span>
        <button class="lg-alt-btn" @click="router.replace({ name: 'recommend' })">先逛逛</button>
      </div>

      <p class="lg-note mute t-f1">
        继续即表示同意用户协议与隐私政策。登录信息仅保存在本机。
      </p>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  background:
    radial-gradient(900px 500px at 20% 0%, var(--c-brand-dim), transparent 70%),
    var(--c-bg);
  padding: var(--s-6);
  overflow-y: auto;
}

.lg-card {
  position: relative;
  width: 100%;
  max-width: 380px;
  padding: var(--s-6);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-4);
  box-shadow: var(--shadow-3);
}

.lg-back {
  position: absolute;
  left: var(--s-3);
  top: var(--s-3);
  width: 28px;
  height: 28px;
  border-radius: 50%;
  font-size: var(--f-6);
  color: var(--c-text-mute);
  line-height: 1;
  transition: background var(--t-fast), color var(--t-fast);
}
.lg-back:hover {
  background: var(--c-bg-hover);
  color: var(--c-text);
}

.lg-brand {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  margin: var(--s-4) 0 var(--s-6);
}
.lg-logo {
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  border-radius: var(--r-3);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--f-6);
  font-weight: 700;
  color: #fff;
  background: var(--c-brand);
}
.lg-title {
  font-size: var(--f-6);
  font-weight: 650;
  line-height: 1.2;
}
.lg-sub {
  font-size: var(--f-2);
  color: var(--c-text-mute);
  margin-top: 2px;
}

.lg-err {
  padding: var(--s-2) var(--s-3);
  margin-bottom: var(--s-3);
  font-size: var(--f-2);
  color: var(--c-err);
  background: var(--c-err-dim);
  border-radius: var(--r-2);
}

.lg-label {
  display: block;
  font-size: var(--f-2);
  color: var(--c-text-dim);
  margin-bottom: var(--s-2);
}
.lg-hint {
  font-size: var(--f-2);
  color: var(--c-text-mute);
  margin-bottom: var(--s-2);
}

.lg-field {
  display: flex;
  align-items: center;
  height: 44px;
  padding: 0 var(--s-3);
  background: var(--c-bg-input);
  border: 1px solid var(--c-line);
  border-radius: var(--r-2);
  transition: border-color var(--t-fast);
  margin-bottom: var(--s-4);
}
.lg-field:focus-within {
  border-color: var(--c-brand);
}

.lg-prefix {
  flex: 0 0 auto;
  font-size: var(--f-3);
  color: var(--c-text-mute);
  padding-right: var(--s-2);
  border-right: 1px solid var(--c-line);
  margin-right: var(--s-2);
}

.lg-input {
  flex: 1 1 auto;
  min-width: 0;
  height: 100%;
  background: none;
  border: none;
  outline: none;
  font-size: var(--f-4);
  color: var(--c-text);
  letter-spacing: 0.5px;
}
.lg-input::placeholder {
  color: var(--c-text-mute);
  letter-spacing: 0;
}
.lg-input-code {
  letter-spacing: 6px;
  font-family: var(--font-num);
}

.lg-resend {
  flex: 0 0 auto;
  font-size: var(--f-2);
  color: var(--c-brand);
  white-space: nowrap;
}
.lg-resend:disabled {
  color: var(--c-text-mute);
  cursor: default;
}

.lg-submit {
  width: 100%;
}

.lg-alt {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--s-2);
  margin-top: var(--s-5);
}
.lg-alt-btn {
  font-size: var(--f-2);
  color: var(--c-text-dim);
  transition: color var(--t-fast);
}
.lg-alt-btn:hover {
  color: var(--c-brand);
}
.lg-alt-sep {
  color: var(--c-text-mute);
}

.lg-note {
  margin-top: var(--s-5);
  text-align: center;
  line-height: 1.6;
}
</style>
