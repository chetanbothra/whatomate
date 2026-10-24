<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { leadSourcesService } from '@/services/api'
import type { LeadSource } from '@/types/leads'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { PageHeader, DataTable, RefreshButton, type Column } from '@/components/shared'
import { toast } from 'vue-sonner'
import { Plus, CheckCircle2, XCircle, ArrowRight, Trash2 } from 'lucide-vue-next'
import { formatDate } from '@/lib/utils'
import { getErrorMessage } from '@/lib/api-utils'

const router = useRouter()
const { t } = useI18n()

const leadSources = ref<LeadSource[]>([])
const isLoading = ref(false)
const isSubmitting = ref(false)
const isCreateDialogOpen = ref(false)

const createForm = ref({
  name: '',
  page_id: '',
  page_access_token: '',
  app_id: '',
  app_secret: '',
  default_country_code: '91',
  api_version: 'v21.0'
})

const columns: Column<LeadSource>[] = [
  { key: 'page_name', label: t('leadAds.pageName'), sortable: true },
  { key: 'page_id', label: t('leadAds.pageId'), sortable: true },
  { key: 'subscribed', label: t('leadAds.subscribed') },
  { key: 'is_active', label: t('leadAds.status') },
  { key: 'last_event_at', label: t('leadAds.lastLead') },
  { key: 'actions', label: t('leadAds.actions'), align: 'right' }
]

async function fetchSources() {
  isLoading.value = true
  try {
    const res = await leadSourcesService.list()
    leadSources.value = (res.data as any).data || res.data || []
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to load lead sources'))
  } finally {
    isLoading.value = false
  }
}

async function handleCreateSource() {
  if (!createForm.value.name || !createForm.value.page_id || !createForm.value.page_access_token) {
    toast.error('Page Name, Page ID, and Page Access Token are required')
    return
  }

  isSubmitting.value = true
  try {
    const res = await leadSourcesService.create(createForm.value)
    toast.success('Facebook Page connected successfully')
    isCreateDialogOpen.value = false
    createForm.value = {
      name: '',
      page_id: '',
      page_access_token: '',
      app_id: '',
      app_secret: '',
      default_country_code: '91',
      api_version: 'v21.0'
    }
    const created = (res.data as any).data || res.data
    await fetchSources()
    if (created?.id) {
      router.push(`/settings/lead-ads/${created.id}`)
    }
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to connect page'))
  } finally {
    isSubmitting.value = false
  }
}

async function toggleActive(source: LeadSource) {
  try {
    const newActive = !source.is_active
    await leadSourcesService.update(source.id, { is_active: newActive })
    source.is_active = newActive
    toast.success(newActive ? 'Lead source activated' : 'Lead source deactivated')
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to toggle status'))
  }
}

async function deleteSource(source: LeadSource) {
  if (!confirm(`Are you sure you want to disconnect ${source.name}?`)) return
  try {
    await leadSourcesService.delete(source.id)
    toast.success('Lead source disconnected')
    fetchSources()
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to delete lead source'))
  }
}

onMounted(() => {
  fetchSources()
})
</script>

<template>
  <div class="space-y-6">
    <PageHeader
      :title="t('leadAds.title')"
      :description="t('leadAds.subtitle')"
    >
      <template #actions>
        <RefreshButton :is-loading="isLoading" @refresh="fetchSources" />
        <Button @click="isCreateDialogOpen = true">
          <Plus class="mr-2 h-4 w-4" />
          {{ t('leadAds.connectPage') }}
        </Button>
      </template>
    </PageHeader>

    <DataTable
      :columns="columns"
      :items="leadSources"
      :is-loading="isLoading"
      :empty-title="t('leadAds.noSources')"
      :empty-description="t('leadAds.subtitle')"
    >
      <template #cell-page_name="{ item }">
        <div class="font-medium">
          {{ item.page_name || item.name }}
        </div>
        <div v-if="item.page_name && item.name !== item.page_name" class="text-xs text-muted-foreground">
          {{ item.name }}
        </div>
      </template>

      <template #cell-page_id="{ item }">
        <code class="rounded bg-muted px-1.5 py-0.5 text-xs font-mono">{{ item.page_id }}</code>
      </template>

      <template #cell-subscribed="{ item }">
        <Badge v-if="item.subscribed" variant="default" class="bg-emerald-600 hover:bg-emerald-700">
          <CheckCircle2 class="mr-1 h-3 w-3" />
          {{ t('leadAds.subscribed') }}
        </Badge>
        <Badge v-else variant="secondary">
          <XCircle class="mr-1 h-3 w-3" />
          Not Subscribed
        </Badge>
      </template>

      <template #cell-is_active="{ item }">
        <div class="flex items-center space-x-2">
          <Switch :checked="item.is_active" @update:checked="toggleActive(item as any)" />
          <span class="text-xs">{{ item.is_active ? t('leadAds.active') : t('leadAds.inactive') }}</span>
        </div>
      </template>

      <template #cell-last_event_at="{ item }">
        <span v-if="item.last_event_at" class="text-xs text-muted-foreground">
          {{ formatDate(item.last_event_at) }}
        </span>
        <span v-else class="text-xs text-muted-foreground">Never</span>
      </template>

      <template #cell-actions="{ item }">
        <div class="flex items-center justify-end space-x-2">
          <Button variant="outline" size="sm" @click="router.push(`/settings/lead-ads/${item.id}`)">
            Configure
            <ArrowRight class="ml-1 h-3.5 w-3.5" />
          </Button>
          <Button variant="ghost" size="icon" @click="deleteSource(item as any)">
            <Trash2 class="h-4 w-4 text-destructive" />
          </Button>
        </div>
      </template>
    </DataTable>

    <!-- Connect Page Dialog -->
    <Dialog v-model:open="isCreateDialogOpen">
      <DialogContent class="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>{{ t('leadAds.connectTitle') }}</DialogTitle>
          <DialogDescription>
            Enter your Facebook Page details and system-user token with leads_retrieval scope.
          </DialogDescription>
        </DialogHeader>

        <form @submit.prevent="handleCreateSource" class="space-y-4 py-2">
          <div class="space-y-1">
            <Label for="src-name">{{ t('common.name') }}</Label>
            <Input id="src-name" v-model="createForm.name" placeholder="e.g. Deepika Jain Official" required />
          </div>

          <div class="space-y-1">
            <Label for="src-page-id">{{ t('leadAds.pageId') }}</Label>
            <Input id="src-page-id" v-model="createForm.page_id" placeholder="e.g. 102938475612345" required />
          </div>

          <div class="space-y-1">
            <Label for="src-token">{{ t('leadAds.pageAccessToken') }}</Label>
            <Input id="src-token" type="password" v-model="createForm.page_access_token" placeholder="EAA..." required />
          </div>

          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label for="src-app-id">{{ t('leadAds.appId') }}</Label>
              <Input id="src-app-id" v-model="createForm.app_id" placeholder="Meta App ID" />
            </div>
            <div class="space-y-1">
              <Label for="src-app-secret">{{ t('leadAds.appSecret') }}</Label>
              <Input id="src-app-secret" type="password" v-model="createForm.app_secret" placeholder="App Secret for HMAC" />
            </div>
          </div>

          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label for="src-cc">{{ t('leadAds.defaultCountryCode') }}</Label>
              <Input id="src-cc" v-model="createForm.default_country_code" placeholder="91" />
            </div>
            <div class="space-y-1">
              <Label for="src-api-ver">Graph API Version</Label>
              <Input id="src-api-ver" v-model="createForm.api_version" placeholder="v21.0" />
            </div>
          </div>

          <DialogFooter class="pt-4">
            <Button type="button" variant="outline" @click="isCreateDialogOpen = false">
              {{ t('common.cancel') }}
            </Button>
            <Button type="submit" :disabled="isSubmitting">
              {{ isSubmitting ? t('common.loading') : t('leadAds.connectPage') }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </div>
</template>
