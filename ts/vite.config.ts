import {defineConfig} from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
 plugins: [vue()],
 build: {
  lib: {entry: {index: 'src/index.ts', vue: 'src/vue.ts', native: 'src/native.ts'}, formats: ['es'], cssFileName: 'style'},
  cssCodeSplit: false,
  rollupOptions: {external: [/^vue$/, /^@synopackageland\/ui($|\/)/, /^@lucide\/vue($|\/)/]},
 },
 test: {include: ['src/**/*.test.ts', 'test/**/*.test.ts']},
})
