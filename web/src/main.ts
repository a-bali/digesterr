import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { router } from './router'
import App from './App.vue'
import './index.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)

// Directive: adds target="_blank" rel="noopener noreferrer" to all <a> tags
// injected via v-html. Vue's template compiler doesn't process v-html content,
// so injected links don't get target attributes automatically.
app.directive('external-links', {
  mounted(el: HTMLElement) {
    el.querySelectorAll('a:not([target])').forEach(a => {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer')
    })
  },
  updated(el: HTMLElement) {
    el.querySelectorAll('a:not([target])').forEach(a => {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer')
    })
  },
})

app.mount('#app')
