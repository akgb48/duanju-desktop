<script setup>
/**
 * 剧集卡片。
 *
 * 两种形态：
 *   - 竖版（默认）：封面 3:4，适合信息流网格
 *   - 横版（landscape）：封面 16:9，适合「继续观看」「相关推荐」条带
 *
 * 封面为空时渲染占位渐变 + 首字，避免出现破图。
 */
import { computed } from 'vue'

const props = defineProps({
  /** FeedItem 或 Series；兼容两种字段命名。 */
  item: { type: Object, required: true },
  /** 竖版 / 横版 */
  layout: { type: String, default: 'poster' },
  /** 是否显示简介 */
  showDesc: { type: Boolean, default: false },
})

const title = computed(() => props.item.title || props.item.Title || '未命名')
const cover = computed(() => props.item.cover || props.item.Cover || '')
const score = computed(() => props.item.score || props.item.Score || '')
const category = computed(() => props.item.category || props.item.Category || '')
const badge = computed(() => props.item.badge || props.item.Badge || '')
const subtitle = computed(() => props.item.subtitle || props.item.Subtitle || props.item.description || '')
const playCount = computed(() => {
  const n = Number(props.item.playCount || props.item.PlayCnt || props.item.PlayCount || 0)
  return formatCount(n)
})

/** 首字占位。 */
const initial = computed(() => (title.value || '?').slice(0, 1))

/** 播放量格式化：1.2万 / 3.4亿。 */
function formatCount(n) {
  if (!n || n < 0) return ''
  if (n >= 100000000) return `${(n / 100000000).toFixed(1)}亿`
  if (n >= 10000) return `${(n / 10000).toFixed(1)}万`
  return String(n)
}

/** 主题色占位：按标题哈希取一个稳定的色相。 */
const placeholderHue = computed(() => {
  let h = 0
  const s = title.value
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360
  return h
})
</script>

<template>
  <article class="vcard" :class="`is-${layout}`">
    <div class="vc-cover">
      <img v-if="cover" :src="cover" :alt="title" loading="lazy" decoding="async" />
      <div
        v-else
        class="vc-placeholder"
        :style="{
          background: `linear-gradient(140deg, hsl(${placeholderHue} 42% 26%), hsl(${(placeholderHue + 40) % 360} 38% 16%))`,
        }"
      >
        <span>{{ initial }}</span>
      </div>

      <span v-if="badge" class="vc-badge">{{ badge }}</span>
      <span v-if="score" class="vc-score">{{ score }}</span>

      <!-- 悬浮播放提示 -->
      <span class="vc-play" aria-hidden="true">
        <svg width="34" height="34" viewBox="0 0 34 34">
          <circle cx="17" cy="17" r="16" fill="rgba(0,0,0,.45)" />
          <path d="M14 11l10 6-10 6z" fill="#fff" />
        </svg>
      </span>
    </div>

    <div class="vc-meta">
      <h3 class="vc-title ellipsis" :title="title">{{ title }}</h3>
      <p v-if="showDesc && subtitle" class="vc-desc clamp-2">{{ subtitle }}</p>
      <div class="vc-sub">
        <span v-if="category" class="tag">{{ category }}</span>
        <span v-if="playCount" class="vc-plays t-num">{{ playCount }}次播放</span>
      </div>
    </div>
  </article>
</template>

<style scoped>
.vcard {
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
  cursor: pointer;
  min-width: 0;
}

.vc-cover {
  position: relative;
  border-radius: var(--r-3);
  overflow: hidden;
  background: var(--c-bg-hover);
  transition: transform var(--t-base), box-shadow var(--t-base);
}
.is-poster .vc-cover {
  aspect-ratio: 3 / 4;
}
.is-landscape .vc-cover {
  aspect-ratio: 16 / 9;
}

.vc-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: transform var(--t-slow);
}

.vcard:hover .vc-cover {
  box-shadow: var(--shadow-2);
  transform: translateY(-2px);
}
.vcard:hover .vc-cover img {
  transform: scale(1.04);
}

.vc-placeholder {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}
.vc-placeholder span {
  font-size: 40px;
  font-weight: 700;
  color: rgba(255, 255, 255, 0.35);
  user-select: none;
}

.vc-badge {
  position: absolute;
  left: var(--s-2);
  top: var(--s-2);
  height: 18px;
  padding: 0 6px;
  display: inline-flex;
  align-items: center;
  font-size: var(--f-1);
  color: #fff;
  background: rgba(0, 0, 0, 0.55);
  border-radius: var(--r-1);
  backdrop-filter: blur(4px);
}

.vc-score {
  position: absolute;
  right: var(--s-2);
  bottom: var(--s-2);
  font-family: var(--font-num);
  font-size: var(--f-2);
  font-weight: 600;
  color: var(--c-accent);
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.8);
}

.vc-play {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transition: opacity var(--t-base);
}
.vcard:hover .vc-play {
  opacity: 1;
}

.vc-meta {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.vc-title {
  font-size: var(--f-3);
  font-weight: 500;
  color: var(--c-text);
}

.vc-desc {
  font-size: var(--f-2);
  color: var(--c-text-mute);
  line-height: 1.45;
}

.vc-sub {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  min-width: 0;
}

.vc-plays {
  font-size: var(--f-1);
  color: var(--c-text-mute);
}
</style>
