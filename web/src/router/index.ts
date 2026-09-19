import { createRouter, createWebHistory } from 'vue-router'
import { getAuthToken, silentRefresh } from '../services/api'
import HomeView from '../views/HomeView.vue'
import LoginView from '../views/LoginView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView },
    { path: '/', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/category/:category', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/category/:category/:itemId(\\d+)', component: HomeView, meta: { requiresAuth: true } },
    { path: '/digests', component: HomeView, meta: { requiresAuth: true } },
    { path: '/digests/:configId(\\d+)/:resultId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    { path: '/saved', component: HomeView, meta: { requiresAuth: true } },
  ],
})

// The access token is memory-only, so a reload always starts with no token —
// but the HttpOnly refresh cookie may still hold a session. Guard the route
// with a silent refresh attempt before sending the user to /login, or every
// reload would log them out.
router.beforeEach(async (to) => {
  if (!to.meta.requiresAuth) return
  if (getAuthToken()) return
  if (await silentRefresh()) return
  return '/login'
})
