import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'

export default [
  {
    // 正式界面资源由脚本构建产出，不参与 lint。
    ignores: ['dist/**', 'editable-dist/**', 'assets/**', 'node_modules/**'],
  },
  js.configs.recommended,
  ...pluginVue.configs['flat/essential'],
  {
    files: ['**/*.{js,mjs,vue}'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
    },
    rules: {
      // 运行环境由 WebView2 注入，全局对象由 Wails 桥接层提供，
      // 这里不做 no-undef 检查，避免为每个浏览器 API 维护 globals 清单。
      'no-undef': 'off',
      // 渐进接入：先把可疑项暴露为警告，不阻塞构建。
      'no-unused-vars': ['warn', { args: 'none', varsIgnorePattern: '^_' }],
      'no-empty': 'warn',
      // 路由页面组件按页面命名（Recommend.vue、Search.vue 等），单词名是刻意为之。
      'vue/multi-word-component-names': 'off',
    },
  },
]
