<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { leadsService, leadSourcesService } from '@/services/api'
import { wsService } from '@/services/websocket'
import type { Lead, LeadStats, LeadSource } from '@/types/leads'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { PageHeader, DataTable, RefreshButton, type Column } from '@/components/shared'
import { toast } from 'vue-sonner'
import {
  RotateCcw,
  Send,
  MessageSquare,
  Trash2,
  Inbox,
  CheckCircle2,
  Clock,
  AlertCircle,
  ChevronRight
} from 'lucide-vue-next'
import { formatDate } from '@/lib/utils'
import { getErrorMessage } from '@/lib/api-utils'

const router = useRouter()
const { t } = useI18n()

const leads = ref<Lead[]>([])
const stats = ref<LeadStats | null>(null)
const sources = ref<LeadSource[]>([])
const isLoading = ref(false)
const totalLeads = ref(0)
const currentPage = ref(1)
const pageSize = 20

// Filters
const filterSourceId = ref<string>('all')
const filterStatus = ref<string>('all')
const filterPhone = ref<string>('')
const filterFormId = ref<string>('')

// Lead Detail Drawer / Modal
const isDetailOpen = ref(false)
const activeLead = ref<Lead | null>(null)
const isRetrying = ref(false)
const isSending = ref(false)

const columns: Column<Lead>[] = [
  { key: 'lead_created_at', label: 'Time', sortable: true },
  { key: 'name', label: 'Name' },
  { key: 'phone', label: 'Phone' },
  { key: 'form_id', label: 'Form' },
  { key: 'ad_name', label: 'Ad / Campaign' },
  { key: 'status', label: 'Status' },
  { key: 'attempts', label: 'Attempts' },
  { key: 'actions', label: 'Actions', align: 'right' }
]

function getStatusBadge(status: string) {
  switch (status) {
    case 'sent':
      return { label: 'Sent', variant: 'default', class: 'bg-emerald-600 hover:bg-emerald-700' }
    case 'stored':
      return { label: 'Stored', variant: 'secondary', class: 'bg-slate-200 text-slate-800' }
    case 'received':
      return { label: 'Received', variant: 'outline', class: 'text-blue-600 border-blue-300' }
    case 'fetched':
      return { label: 'Fetched', variant: 'outline', class: 'text-indigo-600 border-indigo-300' }
    case 'retry':
      return { label: 'Retry', variant: 'outline', class: 'text-amber-600 border-amber-300' }
    case 'failed':
      return { label: 'Failed', variant: 'destructive', class: '' }
    default:
      if (status.startsWith('skipped_')) {
        return { label: status.replace('skipped_', 'Skipped: '), variant: 'secondary', class: 'text-muted-foreground' }
      }
      return { label: status, variant: 'secondary', class: '' }
  }
}

async function fetchStats() {
  try {
    const res = await leadsService.stats()
    stats.value = (res.data as any).data || res.data
  } catch (e) {}
}

async function fetchSources() {
  try {
    const res = await leadSourcesService.list()
    sources.value = (res.data as any).data || res.data || []
  } catch (e) {}
}

async function fetchLeads() {
  isLoading.value = true
  try {
    const res = await leadsService.list({
      source_id: filterSourceId.value !== 'all' ? filterSourceId.value : undefined,
      status: filterStatus.value !== 'all' ? filterStatus.value : undefined,
      phone: filterPhone.value || undefined,
      form_id: filterFormId.value || undefined,
      page: currentPage.value,
      limit: pageSize
    })
    const data = (res.data as any).data || res.data
    leads.value = data.leads || []
    totalLeads.value = data.total || 0
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to fetch leads'))
  } finally {
    isLoading.value = false
  }
}

function openLeadDetail(lead: Lead) {
  activeLead.value = lead
  isDetailOpen.value = true
}

async function handleRetry(lead: Lead) {
  isRetrying.value = true
  try {
    const res = await leadsService.retry(lead.id)
    const updated = (res.data as any).data || res.data
    if (activeLead.value?.id === lead.id) {
      activeLead.value = updated
    }
    toast.success('Lead queued for retry')
    await fetchLeads()
    await fetchStats()
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to retry lead'))
  } finally {
    isRetrying.value = false
  }
}

async function handleSendNow(lead: Lead) {
  isSending.value = true
  try {
    const res = await leadsService.send(lead.id)
    const updated = (res.data as any).data || res.data
    if (activeLead.value?.id === lead.id) {
      activeLead.value = updated
    }
    toast.success('Template sent')
    await fetchLeads()
    await fetchStats()
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to send template'))
  } finally {
    isSending.value = false
  }
}

async function handleDeleteLead(lead: Lead, purge = false) {
  if (!confirm(purge ? 'Purge all personal lead data and contact metadata?' : 'Delete this lead?')) return
  try {
    await leadsService.delete(lead.id, purge)
    toast.success(purge ? 'Lead data purged' : 'Lead deleted')
    isDetailOpen.value = false
    await fetchLeads()
    await fetchStats()
  } catch (err) {
    toast.error(getErrorMessage(err, 'Delete failed'))
  }
}

// Today count helpers
const todayReceivedCount = computed(() => {
  return stats.value?.today?.find(s => s.status === 'received' || s.status === 'fetched')?.count ?? 0
})
const todaySentCount = computed(() => {
  return stats.value?.today?.find(s => s.status === 'sent')?.count ?? 0
})
const todaySkippedCount = computed(() => {
  return stats.value?.today?.filter(s => s.status.startsWith('skipped_'))?.reduce((acc, curr) => acc + curr.count, 0) ?? 0
})
const todayFailedCount = computed(() => {
  return stats.value?.today?.find(s => s.status === 'failed')?.count ?? 0
})

// WebSocket live updates handler
function onLeadUpdateMessage(msg: any) {
  if (msg?.type === 'lead_update' && msg.payload) {
    const updatedLead = msg.payload as Lead
    const idx = leads.value.findIndex(l => l.id === updatedLead.id)
    if (idx !== -1) {
      leads.value[idx] = updatedLead
    } else {
      leads.value.unshift(updatedLead)
    }
    if (activeLead.value?.id === updatedLead.id) {
      activeLead.value = updatedLead
    }
    fetchStats()
  }
}

onMounted(() => {
  fetchSources()
  fetchStats()
  fetchLeads()
  wsService.on('lead_update', onLeadUpdateMessage)
})

onUnmounted(() => {
  wsService.off('lead_update', onLeadUpdateMessage)
})

watch([filterSourceId, filterStatus, filterPhone, filterFormId], () => {
  currentPage.value = 1
  fetchLeads()
})
</script>

<template>
  <div class="space-y-6">
    <PageHeader
      :title="t('leadAds.leadsTitle')"
      :description="t('leadAds.leadsSubtitle')"
    >
      <template #actions>
        <RefreshButton :is-loading="isLoading" @refresh="fetchLeads" />
        <Button variant="outline" @click="router.push('/settings/lead-ads')">
          Configure Pages
        </Button>
      </template>
    </PageHeader>

    <!-- Top Stats Strip -->
    <div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
      <Card class="bg-card">
        <CardContent class="p-4 flex items-center justify-between">
          <div>
            <p class="text-xs font-medium text-muted-foreground">{{ t('leadAds.todayReceived') }}</p>
            <h3 class="text-2xl font-bold mt-1">{{ todayReceivedCount }}</h3>
          </div>
          <Inbox class="h-8 w-8 text-blue-500 opacity-80" />
        </CardContent>
      </Card>
      <Card class="bg-card">
        <CardContent class="p-4 flex items-center justify-between">
          <div>
            <p class="text-xs font-medium text-muted-foreground">{{ t('leadAds.todaySent') }}</p>
            <h3 class="text-2xl font-bold mt-1 text-emerald-600">{{ todaySentCount }}</h3>
          </div>
          <CheckCircle2 class="h-8 w-8 text-emerald-500 opacity-80" />
        </CardContent>
      </Card>
      <Card class="bg-card">
        <CardContent class="p-4 flex items-center justify-between">
          <div>
            <p class="text-xs font-medium text-muted-foreground">{{ t('leadAds.todaySkipped') }}</p>
            <h3 class="text-2xl font-bold mt-1 text-amber-600">{{ todaySkippedCount }}</h3>
          </div>
          <Clock class="h-8 w-8 text-amber-500 opacity-80" />
        </CardContent>
      </Card>
      <Card class="bg-card">
        <CardContent class="p-4 flex items-center justify-between">
          <div>
            <p class="text-xs font-medium text-muted-foreground">{{ t('leadAds.todayFailed') }}</p>
            <h3 class="text-2xl font-bold mt-1 text-rose-600">{{ todayFailedCount }}</h3>
          </div>
          <AlertCircle class="h-8 w-8 text-rose-500 opacity-80" />
        </CardContent>
      </Card>
    </div>

    <!-- Filters Strip -->
    <div class="flex flex-wrap items-center gap-3">
      <div class="w-48">
        <Select v-model="filterSourceId">
          <SelectTrigger>
            <SelectValue placeholder="All Pages" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Pages</SelectItem>
            <SelectItem v-for="s in sources" :key="s.id" :value="s.id">
              {{ s.page_name || s.name }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="w-44">
        <Select v-model="filterStatus">
          <SelectTrigger>
            <SelectValue placeholder="All Statuses" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Statuses</SelectItem>
            <SelectItem value="sent">Sent</SelectItem>
            <SelectItem value="stored">Stored</SelectItem>
            <SelectItem value="received">Received</SelectItem>
            <SelectItem value="fetched">Fetched</SelectItem>
            <SelectItem value="retry">Retry</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
            <SelectItem value="skipped_duplicate">Skipped (Duplicate)</SelectItem>
            <SelectItem value="skipped_invalid_phone">Skipped (Invalid Phone)</SelectItem>
            <SelectItem value="skipped_no_consent">Skipped (No Consent)</SelectItem>
            <SelectItem value="skipped_opted_out">Skipped (Opted Out)</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="w-52">
        <Input v-model="filterPhone" placeholder="Search phone number..." />
      </div>

      <div class="w-44">
        <Input v-model="filterFormId" placeholder="Filter by Form ID..." />
      </div>
    </div>

    <!-- Leads Table -->
    <DataTable
      :columns="columns"
      :items="leads"
      :is-loading="isLoading"
      :server-pagination="true"
      :total-items="totalLeads"
      :page-size="pageSize"
      v-model:current-page="currentPage"
      @page-change="fetchLeads"
      empty-title="No leads received"
      empty-description="Instant-form leads from connected Facebook Pages will show up here automatically."
    >
      <template #cell-lead_created_at="{ item }">
        <span class="text-xs font-mono text-muted-foreground">
          {{ formatDate(item.lead_created_at || item.created_at) }}
        </span>
      </template>

      <template #cell-name="{ item }">
        <div class="font-medium text-sm">{{ item.name || '—' }}</div>
        <div v-if="item.email" class="text-xs text-muted-foreground">{{ item.email }}</div>
      </template>

      <template #cell-phone="{ item }">
        <code class="font-mono text-xs">{{ item.phone || item.phone_raw || '—' }}</code>
      </template>

      <template #cell-form_id="{ item }">
        <div class="text-xs truncate max-w-[120px]">
          {{ item.mapping?.form_name || item.form_id || '—' }}
        </div>
      </template>

      <template #cell-ad_name="{ item }">
        <div class="text-xs font-medium truncate max-w-[150px]">{{ item.ad_name || '—' }}</div>
        <div v-if="item.campaign_name" class="text-xs text-muted-foreground truncate max-w-[150px]">
          {{ item.campaign_name }}
        </div>
      </template>

      <template #cell-status="{ item }">
        <Badge :variant="getStatusBadge(item.status).variant as any" :class="getStatusBadge(item.status).class">
          {{ getStatusBadge(item.status).label }}
        </Badge>
      </template>

      <template #cell-attempts="{ item }">
        <span class="text-xs font-mono">{{ item.attempts }}</span>
      </template>

      <template #cell-actions="{ item }">
        <div class="flex items-center justify-end space-x-1">
          <Button variant="ghost" size="sm" @click="openLeadDetail(item as any)">
            Details
            <ChevronRight class="ml-1 h-3.5 w-3.5" />
          </Button>
        </div>
      </template>
    </DataTable>

    <!-- Lead Detail Modal -->
    <Dialog v-model:open="isDetailOpen">
      <DialogContent class="max-w-2xl max-h-[85vh] overflow-y-auto" v-if="activeLead">
        <DialogHeader>
          <DialogTitle class="flex items-center space-x-2">
            <span>{{ activeLead.name || 'Lead Details' }}</span>
            <Badge :variant="getStatusBadge(activeLead.status).variant as any" :class="getStatusBadge(activeLead.status).class">
              {{ getStatusBadge(activeLead.status).label }}
            </Badge>
          </DialogTitle>
          <DialogDescription>
            Leadgen ID: <code>{{ activeLead.leadgen_id }}</code> • Received {{ formatDate(activeLead.lead_created_at) }}
          </DialogDescription>
        </DialogHeader>

        <div class="space-y-4 py-2">
          <!-- Contact & Ad Info -->
          <div class="grid grid-cols-2 gap-3 rounded-lg border p-3 text-xs bg-muted/20">
            <div>
              <span class="text-muted-foreground">Phone:</span>
              <p class="font-mono font-medium">{{ activeLead.phone || activeLead.phone_raw }}</p>
            </div>
            <div>
              <span class="text-muted-foreground">Email:</span>
              <p class="font-medium">{{ activeLead.email || '—' }}</p>
            </div>
            <div>
              <span class="text-muted-foreground">Campaign:</span>
              <p class="font-medium">{{ activeLead.campaign_name || '—' }}</p>
            </div>
            <div>
              <span class="text-muted-foreground">Ad Name:</span>
              <p class="font-medium">{{ activeLead.ad_name || '—' }}</p>
            </div>
          </div>

          <!-- Error Message if failed or retry -->
          <div v-if="activeLead.last_error" class="rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-xs text-destructive">
            <strong>Error:</strong> {{ activeLead.last_error }}
          </div>

          <!-- Form Answers -->
          <div class="space-y-2">
            <h4 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Form Responses</h4>
            <div class="rounded-lg border divide-y text-xs">
              <div
                v-for="(val, key) in activeLead.field_data"
                :key="key"
                class="flex items-start justify-between p-2.5"
              >
                <span class="font-mono font-medium text-muted-foreground">{{ key }}</span>
                <span class="font-sans text-right max-w-[65%]">{{ val }}</span>
              </div>
              <div v-if="!activeLead.field_data || Object.keys(activeLead.field_data).length === 0" class="p-3 text-center text-muted-foreground">
                No question responses recorded.
              </div>
            </div>
          </div>

          <!-- Navigation Links to Chat and Message -->
          <div class="flex flex-wrap items-center gap-3 pt-2">
            <Button
              v-if="activeLead.contact_id"
              variant="outline"
              size="sm"
              @click="router.push(`/chat/${activeLead.contact_id}`)"
            >
              <MessageSquare class="mr-1.5 h-3.5 w-3.5" />
              {{ t('leadAds.viewChat') }}
            </Button>

            <Button
              variant="outline"
              size="sm"
              :disabled="isRetrying"
              @click="handleRetry(activeLead)"
            >
              <RotateCcw class="mr-1.5 h-3.5 w-3.5" :class="{ 'animate-spin': isRetrying }" />
              {{ t('leadAds.retry') }}
            </Button>

            <Button
              variant="default"
              size="sm"
              :disabled="isSending"
              @click="handleSendNow(activeLead)"
            >
              <Send class="mr-1.5 h-3.5 w-3.5" />
              {{ t('leadAds.sendNow') }}
            </Button>

            <Button
              variant="ghost"
              size="sm"
              class="text-rose-600 hover:text-rose-700 hover:bg-rose-50"
              @click="handleDeleteLead(activeLead, true)"
            >
              <Trash2 class="mr-1.5 h-3.5 w-3.5" />
              {{ t('leadAds.purgeLead') }}
            </Button>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" @click="isDetailOpen = false">{{ t('common.close') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
