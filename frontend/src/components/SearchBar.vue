<script setup>
/**
 * 搜索框。
 *
 * 带联想（SearchSuggest，去抖 220ms），支持：
 *   - 回车提交
 *   - 上下键在联想里导航
 *   - Esc 收起
 *   - 清空按钮
 *
 * 组件不负责跳转，提交时 emit('search', keyword)，由页面决定去哪。
 */
import { ref, watch, onBeforeUnmount, nextTick } from 'vue'
import { SearchSuggest } from '../api/bridge'

const props = defineProps({
  /** 初始关键词 */
  modelValue: { type: String, default: '' },
  /** 占位文案 */
  placeholder: { type: String, default: '搜剧名、演员、类型' },
  /** 是否自动聚焦 */
  autofocus: { type: Boolean, default: false },
  /** 尺寸 */
  size: { type: String, default: 'md' }, // md | lg
})

const emit = defineEmits(['update:modelValue', 'search', 'clear'])

const kw = ref(props.modelValue || '')
const suggests = ref([])
const open = ref(false)
const activeIdx = ref(-1)
const loadingSuggest = ref(false)
const inputEl = ref(null)
const boxEl = ref(null)

let debounceTimer = null
/** 请求序号：丢弃过期响应。 */
let reqSeq = 0

watch(
  () => props.modelValue,
  (v) => {
    if (v !== kw.value) kw.value = v || ''
  },
)

watch(kw, (v) => {
  emit('update:modelValue', v)
  activeIdx.value = -1
  if (debounceTimer) clearTimeout(debounceTimer)
  const q = (v || '').trim()
  if (!q) {
    suggests.value = []
    open.value = false
    return
  }
  debounceTimer = setTimeout(() => fetchSuggest(q), 220)
})

async function fetchSuggest(q) {
  const seq = ++reqSeq
  loadingSuggest.value = true
  try {
    const r = await SearchSuggest(q)
    if (seq !== reqSeq) return // 过期响应
    const list = Array.isArray(r) ? r : (r && r.items) || []
    // 后端可能返回字符串数组，也可能返回 {text} 对象
    suggests.value = list
      .map((x) => (typeof x === 'string' ? x : x.text || x.word || x.name || ''))
      .filter(Boolean)
      .slice(0, 8)
    open.value = suggests.value.length > 0
  } catch {
    if (seq === reqSeq) {
      suggests.value = []
      open.value = false
    }
  } finally {
    if (seq === reqSeq) loadingSuggest.value = false
  }
}

function submit(text) {
  const q = (text !== undefined ? text : kw.value).trim()
  if (!q) return
  kw.value = q
  open.value = false
  emit('search', q)
}

function onKeydown(e) {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    if (!open.value) {
      open.value = suggests.value.length > 0
      return
    }
    activeIdx.value = Math.min(activeIdx.value + 1, suggests.value.length - 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeIdx.value = Math.max(activeIdx.value - 1, -1)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (activeIdx.value >= 0 && suggests.value[activeIdx.value]) {
      submit(suggests.value[activeIdx.value])
    } else {
      submit()
    }
  } else if (e.key === 'Escape') {
    open.value = false
    activeIdx.value = -1
  }
}

function pick(s) {
  submit(s)
  nextTick(() => {
    if (inputEl.value) inputEl.value.blur()
  })
}

function clear() {
  kw.value = ''
  suggests.value = []
  open.value = false
  emit('clear')
  if (inputEl.value) inputEl.value.focus()
}

/** 点击外部收起。 */
function onDocClick(e) {
  if (boxEl.value && !boxEl.value.contains(e.target)) open.value = false
}

watch(open, (v) => {
  if (v) document.addEventListener('mousedown', onDocClick)
  else document.removeEventListener('mousedown', onDocClick)
})

onBeforeUnmount(() => {
  if (debounceTimer) clearTimeout(debounceTimer)
  document.removeEventListener('mousedown', onDocClick)
})

/** 外部可主动聚焦。 */
function focus() {
  if (inputEl.value) inputEl.value.focus()
}
defineExpose({ focus })
</script>

<template>
  <div ref="boxEl" class="sbar" :class="`is-${size}`">
    <span class="sbar-icon" aria-hidden="true">⌕</span>

    <input
      ref="inputEl"
      v-model="kw"
      class="sbar-input"
      type="text"
      :placeholder="placeholder"
      :autofocus="autofocus"
      spellcheck="false"
      @keydown="onKeydown"
      @focus="open = suggests.length > 0"
    />

    <span v-if="loadingSuggest" class="sbar-loading">
      <span class="spinner" style="width: 12px; height: 12px" />
    </span>

    <button v-if="kw" class="sbar-clear" title="清空" @click="clear">✕</button>

    <button class="sbar-go" @click="submit()">搜索</button>

    <!-- 联想 -->
    <transition name="fade">
      <ul v-if="open && suggests.length" class="sbar-drop">
        <li
          v-for="(s, i) in suggests"
          :key="s + i"
          class="sbar-item"
          :class="{ 'is-active': i === activeIdx }"
          @mouseenter="activeIdx = i"
          @click="pick(s)"
        >
          <span class="sbar-item-icon">⌕</span>
          <span class="ellipsis">{{ s }}</span>
        </li>
      </ul>
    </transition>
  </div>
</template>

<style scoped>
.sbar {
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
  height: 36px;
  padding-left: var(--s-3);
  background: var(--c-bg-input);
  border: 1px solid var(--c-line);
  border-radius: var(--r-full);
  transition: border-color var(--t-fast), background var(--t-fast);
}
.sbar:focus-within {
  border-color: var(--c-brand);
  background: var(--c-bg-elev);
}
.sbar.is-lg {
  height: 44px;
  padding-left: var(--s-4);
}

.sbar-icon {
  flex: 0 0 auto;
  font-size: var(--f-5);
  color: var(--c-text-mute);
  line-height: 1;
}

.sbar-input {
  flex: 1 1 auto;
  min-width: 0;
  height: 100%;
  padding: 0 var(--s-2);
  background: none;
  border: none;
  color: var(--c-text);
  outline: none;
}
.sbar-input::placeholder {
  color: var(--c-text-mute);
}
.sbar.is-lg .sbar-input {
  font-size: var(--f-5);
}

.sbar-loading {
  flex: 0 0 auto;
  display: flex;
  padding-right: var(--s-2);
}

.sbar-clear {
  flex: 0 0 auto;
  width: 22px;
  height: 22px;
  margin-right: var(--s-1);
  border-radius: 50%;
  color: var(--c-text-mute);
  font-size: var(--f-1);
  transition: background var(--t-fast), color var(--t-fast);
}
.sbar-clear:hover {
  background: var(--c-bg-active);
  color: var(--c-text);
}

.sbar-go {
  flex: 0 0 auto;
  height: calc(100% - 6px);
  padding: 0 var(--s-4);
  margin-right: 3px;
  border-radius: var(--r-full);
  font-size: var(--f-2);
  color: #fff;
  background: var(--c-brand);
  transition: background var(--t-fast);
}
.sbar-go:hover {
  background: var(--c-brand-hover);
}

/* ---- 联想下拉 ---- */
.sbar-drop {
  position: absolute;
  left: 0;
  right: 0;
  top: calc(100% + var(--s-1));
  z-index: var(--z-overlay);
  padding: var(--s-1);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line);
  border-radius: var(--r-3);
  box-shadow: var(--shadow-3);
  max-height: 320px;
  overflow-y: auto;
}

.sbar-item {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  height: 34px;
  padding: 0 var(--s-3);
  border-radius: var(--r-2);
  font-size: var(--f-3);
  color: var(--c-text-dim);
  cursor: pointer;
  min-width: 0;
}
.sbar-item.is-active {
  background: var(--c-bg-hover);
  color: var(--c-text);
}
.sbar-item-icon {
  flex: 0 0 auto;
  font-size: var(--f-3);
  color: var(--c-text-mute);
}
</style>
