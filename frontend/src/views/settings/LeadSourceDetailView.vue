<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { leadSourcesService, accountsService, templatesService, tagsService, teamsService, usersService } from '@/services/api'
import type { LeadSource, FormWithMapping, LeadFormMapping } from '@/types/leads'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { PageHeader, RefreshButton } from '@/components/shared'
import { toast } from 'vue-sonner'
import { Copy, Check, CheckCircle2, XCircle, ArrowLeft, RefreshCw, Eye } from 'lucide-vue-next'
import { getErrorMessage } from '@/lib/api-utils'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const sourceId = computed(() => route.params.id as string)
const source = ref<LeadSource | null>(null)
const forms = ref<FormWithMapping[]>([])
const isLoading = ref(false)
const isTesting = ref(false)
const isSubscribing = ref(false)
const testResults = ref<any>(null)
const copiedWebhook = ref(false)
const copiedToken = ref(false)

// Accounts, Templates, Tags, Teams, Users
const accounts = ref<any[]>([])
const approvedTemplates = ref<any[]>([])
const availableTags = ref<any[]>([])
const availableUsers = ref<any[]>([])
const availableTeams = ref<any[]>([])

// Mapping drawer / modal state
const isMappingOpen = ref(false)
const selectedForm = ref<FormWithMapping | null>(null)
const isSavingMapping = ref(false)
const previewText = ref('')

const mappingForm = ref<Partial<LeadFormMapping>>({
  form_id: '',
  form_name: '',
  is_active: true,
  whatsapp_account: '',
  template_id: undefined,
  template_params: {},
  header_params: {},
  button_params: {},
  phone_field: 'phone_number',
  name_field: 'full_name',
  consent_field: '',
  tags: [],
  send_delay_seconds: 0,
  dedupe_hours: 24,
  skip_if_opted_out: true
})

// Backfill state
const backfillFormId = ref('')
const backfillSince = ref('')
const backfillSend = ref(false)
const isBackfilling = ref(false)

async function fetchSourceDetail() {
  isLoading.value = true
  try {
    const res = await leadSourcesService.get(sourceId.value)
    source.value = (res.data as any).data || res.data
    await fetchForms()
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to load lead source'))
  } finally {
    isLoading.value = false
  }
}

async function fetchForms() {
  try {
    const res = await leadSourcesService.forms(sourceId.value)
    forms.value = (res.data as any).data || res.data || []
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to load forms'))
  }
}

async function testConnection() {
  isTesting.value = true
  testResults.value = null
  try {
    const res = await leadSourcesService.test(sourceId.value)
    testResults.value = (res.data as any).data || res.data
    toast.success('Connection test finished')
  } catch (err) {
    toast.error(getErrorMessage(err, 'Connection test failed'))
  } finally {
    isTesting.value = false
  }
}

async function subscribePage() {
  isSubscribing.value = true
  try {
    await leadSourcesService.subscribe(sourceId.value)
    toast.success('Page subscribed to leadgen webhooks successfully')
    if (source.value) source.value.subscribed = true
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to subscribe page'))
  } finally {
    isSubscribing.value = false
  }
}

function copyToClipboard(text: string, type: 'webhook' | 'token') {
  navigator.clipboard.writeText(text)
  if (type === 'webhook') {
    copiedWebhook.value = true
    setTimeout(() => (copiedWebhook.value = false), 2000)
  } else {
    copiedToken.value = true
    setTimeout(() => (copiedToken.value = false), 2000)
  }
  toast.success(t('common.copied'))
}

// Load auxiliary data for mapping drawer
async function loadAuxData() {
  try {
    const [accRes, tmplRes, tagRes, userRes, teamRes] = await Promise.allSettled([
      accountsService.list(),
      templatesService.list(),
      tagsService.list(),
      usersService.list(),
      teamsService.list()
    ])
    if (accRes.status === 'fulfilled') {
      accounts.value = (accRes.value.data as any).data || accRes.value.data || []
    }
    if (tmplRes.status === 'fulfilled') {
      const allTemplates = (tmplRes.value.data as any).data || tmplRes.value.data || []
      approvedTemplates.value = allTemplates.filter((t: any) => t.status === 'APPROVED')
    }
    if (tagRes.status === 'fulfilled') {
      availableTags.value = (tagRes.value.data as any).data || tagRes.value.data || []
    }
    if (userRes.status === 'fulfilled') {
      availableUsers.value = (userRes.value.data as any).data || userRes.value.data || []
    }
    if (teamRes.status === 'fulfilled') {
      availableTeams.value = (teamRes.value.data as any).data || teamRes.value.data || []
    }
  } catch (e) {
    // optional loads
  }
}

function openMappingDrawer(form: FormWithMapping) {
  selectedForm.value = form
  const existing = form.mapping
  if (existing) {
    mappingForm.value = {
      ...existing,
      template_params: existing.template_params || {},
      header_params: existing.header_params || {},
      button_params: existing.button_params || {},
      tags: existing.tags || []
    }
  } else {
    mappingForm.value = {
      form_id: form.id,
      form_name: form.name,
      is_active: true,
      whatsapp_account: '',
      template_id: undefined,
      template_params: {},
      header_params: {},
      button_params: {},
      phone_field: 'phone_number',
      name_field: 'full_name',
      consent_field: '',
      tags: [],
      send_delay_seconds: 0,
      dedupe_hours: 24,
      skip_if_opted_out: true
    }
  }
  previewText.value = ''
  isMappingOpen.value = true
}

// Selected template object
const currentTemplate = computed(() => {
  if (!mappingForm.value.template_id) return null
  return approvedTemplates.value.find((t: any) => t.id === mappingForm.value.template_id)
})

// Extract parameter list like {{1}}, {{2}} from template body
const templateVariables = computed<string[]>(() => {
  if (!currentTemplate.value?.body) return []
  const matches = (currentTemplate.value.body as string).match(/\{\{([^}]+)\}\}/g) || []
  return Array.from(new Set(matches.map((m: string) => m.replace(/[{}]/g, '').trim())))
})

watch(() => mappingForm.value.template_id, () => {
  if (currentTemplate.value) {
    const p: Record<string, string> = { ...(mappingForm.value.template_params || {}) }
    for (const v of templateVariables.value) {
      if (!p[v]) {
        p[v] = v === '1' ? '{{if lead.first_name}}{{lead.first_name}}{{else}}there{{endif}}' : ''
      }
    }
    mappingForm.value.template_params = p
  }
})

async function updatePreview() {
  if (!currentTemplate.value) {
    previewText.value = ''
    return
  }
  let body: string = currentTemplate.value.body
  const p = mappingForm.value.template_params || {}
  for (const v of templateVariables.value) {
    const expr = p[v] || `{{${v}}}`
    body = body.split(`{{${v}}}`).join(expr)
  }
  previewText.value = body
}

watch([() => mappingForm.value.template_id, () => mappingForm.value.template_params], () => {
  updatePreview()
}, { deep: true })

async function saveMapping() {
  if (!mappingForm.value.whatsapp_account && mappingForm.value.template_id) {
    toast.error('Please select a WhatsApp account')
    return
  }
  isSavingMapping.value = true
  try {
    if (mappingForm.value.id) {
      await leadSourcesService.updateMapping(mappingForm.value.id, mappingForm.value)
      toast.success('Mapping updated')
    } else {
      await leadSourcesService.createMapping(sourceId.value, mappingForm.value)
      toast.success('Mapping created')
    }
    isMappingOpen.value = false
    await fetchForms()
  } catch (err) {
    toast.error(getErrorMessage(err, 'Failed to save mapping'))
  } finally {
    isSavingMapping.value = false
  }
}

async function handleRunBackfill() {
  isBackfilling.value = true
  try {
    const res = await leadSourcesService.sync(sourceId.value, {
      form_id: backfillFormId.value || undefined,
      since: backfillSince.value ? new Date(backfillSince.value).toISOString() : undefined,
      send: backfillSend.value
    })
    const count = (res.data as any).data?.synced ?? 0
    toast.success(`Backfill complete: ${count} lead(s) synced`)
  } catch (err) {
    toast.error(getErrorMessage(err, 'Backfill failed'))
  } finally {
    isBackfilling.value = false
  }
}

onMounted(() => {
  fetchSourceDetail()
  loadAuxData()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center space-x-4">
      <Button variant="ghost" size="sm" @click="router.push('/settings/lead-ads')">
        <ArrowLeft class="mr-2 h-4 w-4" />
        Back to Pages
      </Button>
    </div>

    <PageHeader
      :title="source?.page_name || source?.name || 'Page Configuration'"
      :description="`Facebook Page ID: ${source?.page_id || ''}`"
    >
      <template #actions>
        <RefreshButton :is-loading="isLoading" @refresh="fetchSourceDetail" />
        <Button variant="outline" :disabled="isTesting" @click="testConnection">
          <RefreshCw class="mr-2 h-4 w-4" :class="{ 'animate-spin': isTesting }" />
          {{ t('leadAds.testConnection') }}
        </Button>
        <Button
          :variant="source?.subscribed ? 'outline' : 'default'"
          :disabled="isSubscribing"
          @click="subscribePage"
        >
          <CheckCircle2 v-if="source?.subscribed" class="mr-2 h-4 w-4 text-emerald-500" />
          {{ source?.subscribed ? 'Subscribed' : t('leadAds.subscribePage') }}
        </Button>
      </template>
    </PageHeader>

    <!-- Checklist results from Test Connection -->
    <div v-if="testResults" class="rounded-lg border bg-card p-4 text-card-foreground shadow-sm">
      <h3 class="mb-3 text-sm font-semibold">Connection Diagnostics</h3>
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <div class="flex items-center space-x-2">
          <CheckCircle2 v-if="testResults.token_valid" class="h-4 w-4 text-emerald-500" />
          <XCircle v-else class="h-4 w-4 text-rose-500" />
          <span class="text-sm">Page Token Valid</span>
        </div>
        <div class="flex items-center space-x-2">
          <CheckCircle2 v-if="testResults.leads_access" class="h-4 w-4 text-emerald-500" />
          <XCircle v-else class="h-4 w-4 text-rose-500" />
          <span class="text-sm">Leads Retrieval Access ({{ testResults.forms_count ?? 0 }} forms)</span>
        </div>
        <div class="flex items-center space-x-2">
          <CheckCircle2 v-if="testResults.subscribed" class="h-4 w-4 text-emerald-500" />
          <XCircle v-else class="h-4 w-4 text-amber-500" />
          <span class="text-sm">Webhook Subscribed</span>
        </div>
      </div>
      <div v-if="testResults.token_error" class="mt-2 text-xs text-rose-500">
        {{ testResults.token_error }}
      </div>
      <div v-if="testResults.leads_access_error" class="mt-2 text-xs text-rose-500">
        {{ testResults.leads_access_error }}
      </div>
    </div>

    <!-- Meta Webhook Configuration Card -->
    <Card>
      <CardHeader>
        <CardTitle>Meta Webhook Settings</CardTitle>
        <CardDescription>
          In Meta App Dashboard &gt; Webhooks &gt; Page, configure this Callback URL and Verify Token, then subscribe to the leadgen field.
        </CardDescription>
      </CardHeader>
      <CardContent class="space-y-4">
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div class="space-y-1">
            <Label>{{ t('leadAds.webhookUrl') }}</Label>
            <div class="flex items-center space-x-2">
              <Input :model-value="source?.webhook_url || ''" readonly class="font-mono text-xs" />
              <Button variant="outline" size="icon" @click="copyToClipboard(source?.webhook_url || '', 'webhook')">
                <Check v-if="copiedWebhook" class="h-4 w-4 text-emerald-500" />
                <Copy v-else class="h-4 w-4" />
              </Button>
            </div>
          </div>
          <div class="space-y-1">
            <Label>{{ t('leadAds.webhookVerifyToken') }}</Label>
            <div class="flex items-center space-x-2">
              <Input :model-value="source?.webhook_verify_token || ''" readonly class="font-mono text-xs" />
              <Button variant="outline" size="icon" @click="copyToClipboard(source?.webhook_verify_token || '', 'token')">
                <Check v-if="copiedToken" class="h-4 w-4 text-emerald-500" />
                <Copy v-else class="h-4 w-4" />
              </Button>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>

    <!-- Forms and Auto-Response Mappings Card -->
    <Card>
      <CardHeader class="flex flex-row items-center justify-between">
        <div>
          <CardTitle>{{ t('leadAds.formsTitle') }}</CardTitle>
          <CardDescription>
            Map each Meta Instant Form to an approved WhatsApp template, variable rules, and tags.
          </CardDescription>
        </div>
        <Button variant="outline" size="sm" @click="openMappingDrawer({ id: '*', name: 'Default (all forms)', status: 'ACTIVE', questions: [], leads_count: 0 })">
          Configure Default (*)
        </Button>
      </CardHeader>
      <CardContent>
        <div class="divide-y rounded-md border">
          <div
            v-for="form in forms"
            :key="form.id"
            class="flex items-center justify-between p-4 hover:bg-muted/50 transition-colors"
          >
            <div class="space-y-1">
              <div class="flex items-center space-x-2">
                <span class="font-medium text-sm">{{ form.name }}</span>
                <Badge :variant="form.mapping ? 'default' : 'secondary'">
                  {{ form.mapping ? t('leadAds.mapped') : t('leadAds.notMapped') }}
                </Badge>
                <Badge variant="outline" class="text-xs">
                  {{ form.status }}
                </Badge>
              </div>
              <div class="text-xs text-muted-foreground">
                Form ID: <code class="font-mono">{{ form.id }}</code> • {{ form.leads_count }} leads collected • {{ form.questions.length }} questions
              </div>
            </div>

            <Button variant="outline" size="sm" @click="openMappingDrawer(form)">
              {{ form.mapping ? 'Edit Auto-Response' : 'Setup Auto-Response' }}
            </Button>
          </div>

          <div v-if="forms.length === 0" class="p-8 text-center text-sm text-muted-foreground">
            No forms detected on this Facebook Page.
          </div>
        </div>
      </CardContent>
    </Card>

    <!-- Backfill / Historical Leads Card -->
    <Card>
      <CardHeader>
        <CardTitle>{{ t('leadAds.backfillTitle') }}</CardTitle>
        <CardDescription>
          Pull existing leads from Meta directly into Whatomate. New leads will be deduplicated automatically.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div class="space-y-1">
            <Label>Form</Label>
            <Select v-model="backfillFormId">
              <SelectTrigger>
                <SelectValue placeholder="All mapped forms" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All mapped forms</SelectItem>
                <SelectItem v-for="f in forms" :key="f.id" :value="f.id">
                  {{ f.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div class="space-y-1">
            <Label>{{ t('leadAds.sinceDate') }}</Label>
            <Input type="datetime-local" v-model="backfillSince" />
          </div>

          <div class="space-y-1">
            <Label>Send WhatsApp</Label>
            <div class="flex items-center space-x-2 pt-2">
              <Switch v-model:checked="backfillSend" />
              <span class="text-xs text-muted-foreground">{{ t('leadAds.alsoSendWhatsApp') }}</span>
            </div>
          </div>
        </div>

        <div class="mt-4 flex justify-end">
          <Button :disabled="isBackfilling" @click="handleRunBackfill">
            <RefreshCw class="mr-2 h-4 w-4" :class="{ 'animate-spin': isBackfilling }" />
            {{ t('leadAds.runBackfill') }}
          </Button>
        </div>
      </CardContent>
    </Card>

    <!-- Form Mapping Drawer / Modal -->
    <Dialog v-model:open="isMappingOpen">
      <DialogContent class="max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ t('leadAds.mappingDrawerTitle') }}</DialogTitle>
          <DialogDescription>
            Target Form: <strong>{{ selectedForm?.name }}</strong> (<code>{{ selectedForm?.id }}</code>)
          </DialogDescription>
        </DialogHeader>

        <div class="space-y-4 py-2">
          <!-- WhatsApp Account -->
          <div class="space-y-1">
            <Label>{{ t('leadAds.whatsappAccount') }}</Label>
            <Select v-model="mappingForm.whatsapp_account">
              <SelectTrigger>
                <SelectValue placeholder="Select outgoing WhatsApp Account" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="acc in accounts" :key="acc.id" :value="acc.name">
                  {{ acc.name }} ({{ acc.display_phone_number || acc.phone_id }})
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <!-- Template Select -->
          <div class="space-y-1">
            <Label>{{ t('leadAds.template') }}</Label>
            <Select
              :model-value="mappingForm.template_id || 'none'"
              @update:model-value="(val) => { mappingForm.template_id = val === 'none' ? undefined : String(val) }"
            >
              <SelectTrigger>
                <SelectValue placeholder="Select an approved template (optional)" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">None (Store lead only, do not send template)</SelectItem>
                <SelectItem v-for="tmpl in approvedTemplates" :key="tmpl.id" :value="tmpl.id">
                  {{ tmpl.name }} ({{ tmpl.language }}) - {{ tmpl.category }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <!-- Template Variables Mapping -->
          <div v-if="templateVariables.length > 0" class="rounded-md border p-3 bg-muted/20 space-y-3">
            <h4 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Template Body Variables</h4>
            <div v-for="v in templateVariables" :key="v" class="space-y-1">
              <div class="flex justify-between items-center text-xs">
                <span>Variable <code v-text="'{{' + v + '}}'"></code></span>
                <span class="text-muted-foreground">e.g. <code>lead.first_name</code> or <code>field.your_question</code></span>
              </div>
              <Input
                v-model="mappingForm.template_params![v]"
                placeholder="{{if lead.first_name}}{{lead.first_name}}{{else}}there{{endif}}"
              />
            </div>
          </div>

          <!-- Live Preview Bubble -->
          <div v-if="previewText" class="rounded-lg border bg-emerald-50/50 dark:bg-emerald-950/20 p-3 space-y-1">
            <div class="flex items-center text-xs font-medium text-emerald-700 dark:text-emerald-400">
              <Eye class="mr-1.5 h-3.5 w-3.5" />
              {{ t('leadAds.previewTitle') }}
            </div>
            <p class="whitespace-pre-wrap text-sm font-sans">{{ previewText }}</p>
          </div>

          <!-- Question Fields Extraction -->
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label>{{ t('leadAds.phoneField') }}</Label>
              <Input v-model="mappingForm.phone_field" placeholder="phone_number" />
            </div>
            <div class="space-y-1">
              <Label>{{ t('leadAds.nameField') }}</Label>
              <Input v-model="mappingForm.name_field" placeholder="full_name" />
            </div>
          </div>

          <div class="space-y-1">
            <Label>{{ t('leadAds.consentField') }}</Label>
            <Input v-model="mappingForm.consent_field" placeholder="Leave empty if consent not explicitly asked" />
          </div>

          <!-- Tags, Assignment, Delay, Dedupe -->
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label>{{ t('leadAds.assignUser') }}</Label>
              <Select
                :model-value="mappingForm.assign_user_id || 'unassigned'"
                @update:model-value="(val) => { mappingForm.assign_user_id = val === 'unassigned' ? undefined : String(val) }"
              >
                <SelectTrigger>
                  <SelectValue placeholder="Unassigned" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="unassigned">Unassigned</SelectItem>
                  <SelectItem v-for="u in availableUsers" :key="u.id" :value="u.id">
                    {{ u.name || u.email }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1">
              <Label>{{ t('leadAds.assignTeam') }}</Label>
              <Select
                :model-value="mappingForm.assign_team_id || 'none'"
                @update:model-value="(val) => { mappingForm.assign_team_id = val === 'none' ? undefined : String(val) }"
              >
                <SelectTrigger>
                  <SelectValue placeholder="No team" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">No team</SelectItem>
                  <SelectItem v-for="tm in availableTeams" :key="tm.id" :value="tm.id">
                    {{ tm.name }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label>{{ t('leadAds.sendDelay') }}</Label>
              <Input type="number" v-model.number="mappingForm.send_delay_seconds" min="0" placeholder="0" />
            </div>
            <div class="space-y-1">
              <Label>{{ t('leadAds.dedupeHours') }}</Label>
              <Input type="number" v-model.number="mappingForm.dedupe_hours" min="0" placeholder="24" />
            </div>
          </div>

          <div class="flex items-center space-x-2 pt-2">
            <Switch v-model:checked="mappingForm.skip_if_opted_out" />
            <span class="text-xs">{{ t('leadAds.skipIfOptedOut') }}</span>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" @click="isMappingOpen = false">{{ t('common.cancel') }}</Button>
          <Button :disabled="isSavingMapping" @click="saveMapping">
            {{ isSavingMapping ? t('common.loading') : t('common.save') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
