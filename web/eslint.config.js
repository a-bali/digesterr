import pluginVue from 'eslint-plugin-vue'
import vueParser from 'vue-eslint-parser'
import tsParser from '@typescript-eslint/parser'
import tsPlugin from '@typescript-eslint/eslint-plugin'
import globals from 'globals'

export default [
  { ignores: ['dist/', 'node_modules/', '.vite/', '*.d.ts', '**/*.test.ts'] },
  { languageOptions: { globals: { ...globals.browser, ...globals.node } } },
  ...pluginVue.configs['flat/essential'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      parser: vueParser,
      parserOptions: { parser: tsParser },
    },
  },
  {
    files: ['**/*.ts'],
    languageOptions: { parser: tsParser },
  },
  {
    rules: {
      'vue/multi-word-component-names': 'off',
      'vue/no-v-html': 'off',
      // TypeScript-aware unused-vars (base rule can't parse TS type params in
      // defineProps/defineEmits and produces false positives).
      'no-unused-vars': 'off',
      '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_' }],
    },
    plugins: { '@typescript-eslint': tsPlugin },
  },
]
