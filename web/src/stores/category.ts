import { defineStore } from 'pinia'
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { categoryApi, ApiError } from '../services/api'
import type { Category } from '../types/api'

export const useCategoryStore = defineStore('category', () => {
  const categories = ref<Category[]>([])
  const loading = ref(false)

  async function fetchCategories() {
    try {
      categories.value = await categoryApi.list()
    } catch (e) {
      toast.error('Failed to load categories', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function create(name: string) {
    const cat = await categoryApi.create(name)
    categories.value.push(cat)
    return cat
  }

  async function update(id: number, name: string) {
    const cat = await categoryApi.update(id, name)
    const idx = categories.value.findIndex(c => c.id === id)
    if (idx !== -1) categories.value[idx] = cat
  }

  async function remove(id: number) {
    await categoryApi.delete(id)
    categories.value = categories.value.filter(c => c.id !== id)
  }

  return { categories, loading, fetchCategories, create, update, remove }
})
