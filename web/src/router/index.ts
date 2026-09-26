import { createRouter, createWebHistory } from 'vue-router'
import { getAuthToken, silentRefresh } from '../services/api'

// Route-level code splitting: the login page (and the 404 page) load without
// pulling in the whole reader shell and its dialogs.
const HomeView = () => import('../views/HomeView.vue')
const LoginView = () => import('../views/LoginView.vue')
const NotFoundView = () => import('../views/NotFoundView.vue')

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView },
    { path: '/', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    // All-Feeds item: the section list has no feed or category to put in the
    // path, so the id needs its own segment. Without it, selecting an item in
    // the section view had nowhere to navigate and the URL could not name the
    // item at all -- the pane updated while the route stayed at /feeds.
    { path: '/feeds/item/:itemId(\\d+)', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/category/:category', component: HomeView, meta: { requiresAuth: true } },
    { path: '/feeds/category/:category/:itemId(\\d+)', component: HomeView, meta: { requiresAuth: true } },
    { path: '/digests', component: HomeView, meta: { requiresAuth: true } },
    { path: '/digests/:configId(\\d+)/:resultId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    // Saved item. Saved deliberately ignores the persisted read-state filter, so
    // it spans feeds and categories exactly like the section view; the bare
    // /saved route could not represent WHICH item was open, so a saved item was
    // unlinkable and a reload lost the selection.
    { path: '/saved/:itemId(\\d+)?', component: HomeView, meta: { requiresAuth: true } },
    // Unknown URLs rendered a blank page (no match, no redirect). The 404 is
    // outside auth on purpose: an unauthenticated typo still needs a page.
    { path: '/:pathMatch(.*)*', component: NotFoundView },
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
