import { createRouter, createWebHistory } from 'vue-router'
import { getAuthToken } from '../services/api'
import HomeView from '../views/HomeView.vue'
import LoginView from '../views/LoginView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView },
    { path: '/', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    { path: '/digests/:configId(\\d+)/:resultId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    { path: '/saved', component: HomeView, meta: { requiresAuth: true } },
  ],
})

router.beforeEach((to) => {
  if (to.meta.requiresAuth && !getAuthToken()) {
    return '/login'
  }
})
