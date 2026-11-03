<script setup>
/**
 * 404。
 *
 * 不在 App.vue 里加 chromeless，让它保留侧栏，用户可以直接点走。
 */
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

const missingPath = computed(() => route.fullPath || '/')

function goHome() {
  router.replace({ name: 'recommend' })
}

function goBack() {
  if (window.history.length > 1) router.back()
  else goHome()
}
</script>

<template>
  <div class="v-root nf">
    <div class="nf-inner">
      <p class="nf-code t-num">404</p>
      <h1 class="nf-title">这个页面不见了</h1>
      <p class="nf-desc">
        你访问的地址可能已经被移除或改名。
      </p>
      <code class="nf-path">{{ missingPath }}</code>

      <div class="nf-acts">
        <button class="btn btn-primary" @click="goHome">回到首页</button>
        <button class="btn" @click="goBack">返回上一页</button>
        <button class="btn btn-ghost" @click="router.push({ name: 'search' })">去搜索</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.nf {
  display: flex;
  align-items: center;
  justify-content: center;
}

.nf-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: var(--s-6);
  max-width: 460px;
}

.nf-code {
  font-size: 76px;
  font-weight: 800;
  line-height: 1;
  letter-spacing: -2px;
  color: var(--c-bg-active);
  background: linear-gradient(180deg, var(--c-brand), var(--c-brand-dim));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  margin-bottom: var(--s-3);
}

.nf-title {
  font-size: var(--f-6);
  font-weight: 650;
  margin-bottom: var(--s-2);
}

.nf-desc {
  font-size: var(--f-3);
  color: var(--c-text-mute);
  line-height: 1.7;
  margin-bottom: var(--s-3);
}

.nf-path {
  display: block;
  max-width: 100%;
  padding: var(--s-1) var(--s-3);
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: var(--f-1);
  color: var(--c-text-mute);
  background: var(--c-bg-elev);
  border: 1px solid var(--c-line-soft);
  border-radius: var(--r-1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-bottom: var(--s-5);
}

.nf-acts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  justify-content: center;
}
</style>
