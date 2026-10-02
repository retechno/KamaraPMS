<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CalculateChargeRequest, ChargeCalculation, PriceMode } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * Preview of the calculation engine for one charge code. It uses the SAVED rules (nothing is posted), so the
 * parent bumps `version` after saving rules and an existing result is recalculated.
 */
const props = defineProps<{ chargeCodeId: number; priceMode: PriceMode; defaultUnitPrice?: string; version: number }>()

const property = usePropertyStore()
const quantity = ref('1')
const unitPrice = ref(props.defaultUnitPrice ? String(Number(props.defaultUnitPrice)) : '')
const discount = ref('')
const modeOverride = ref<'' | PriceMode>('')
const result = ref<ChargeCalculation | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)

const fieldError = (field: string) => error.value?.fieldMessage(field)
const currency = computed(() => property.current?.currency_code ?? '')

async function calculate(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  busy.value = true
  error.value = null
  const body: CalculateChargeRequest = { charge_code_id: props.chargeCodeId, quantity: quantity.value, unit_price: unitPrice.value }
  if (discount.value) body.discount_amount = discount.value
  if (modeOverride.value) body.price_mode = modeOverride.value
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/charge-calculations', { params: { path: { propertyId } }, body })
    result.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    result.value = null
  } finally {
    busy.value = false
  }
}

watch(() => props.version, () => {
  if (result.value) void calculate()
})
</script>

<template>
  <section class="mt-6 border-t border-border pt-4" data-testid="calculator">
    <h2 class="m-0 text-base font-semibold">{{ t('calc.title') }}</h2>
    <p class="mb-3 mt-1 text-sm text-muted-foreground">{{ t('calc.hint') }}</p>
    <p v-if="error && !error.fieldErrors.length" class="alert" role="alert" data-testid="calc-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-5" @keydown.enter.prevent="calculate">
      <FormField :label="t('calc.quantity')" :error="fieldError('quantity')">
        <template #default="{ id, invalid }"><Input :id="id" v-model="quantity" name="quantity" inputmode="decimal" :aria-invalid="invalid" /></template>
      </FormField>
      <FormField :label="t('calc.unitPrice', { currency })" :hint="t('calc.unitPriceHint')" :error="fieldError('unit_price')">
        <template #default="{ id, invalid }"><Input :id="id" v-model="unitPrice" name="unit_price" inputmode="decimal" :aria-invalid="invalid" /></template>
      </FormField>
      <FormField :label="t('calc.discount')" :error="fieldError('discount_amount')">
        <template #default="{ id, invalid }"><Input :id="id" v-model="discount" name="discount_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
      </FormField>
      <FormField :label="t('calc.priceIs')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="modeOverride" name="price_mode">
            <option value="">{{ t('calc.asCode', { mode: priceMode === 'INCLUSIVE' ? t('calc.inclusive').toLowerCase() : t('calc.exclusive').toLowerCase() }) }}</option>
            <option value="EXCLUSIVE">{{ t('calc.exclusive') }}</option>
            <option value="INCLUSIVE">{{ t('calc.inclusive') }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <div class="flex items-end">
        <Button type="button" variant="outline" :disabled="busy" data-testid="calculate" @click="calculate">{{ t('calc.calculate') }}</Button>
      </div>
    </div>

    <table v-if="result" class="mt-4 w-full border-collapse text-sm" data-testid="result">
      <thead>
        <tr class="border-b border-border text-left text-xs text-muted-foreground">
          <th class="py-1.5 pr-3 font-medium">{{ t('calc.line') }}</th>
          <th class="px-3 text-right font-medium">{{ t('calc.rate') }}</th>
          <th class="px-3 text-right font-medium">{{ t('calc.on') }}</th>
          <th class="pl-3 text-right font-medium">{{ t('calc.amount') }}</th>
        </tr>
      </thead>
      <tbody class="[&_td.num]:text-right [&_td.num]:tabular-nums [&_td]:py-1.5 [&_tr]:border-b [&_tr]:border-border">
        <tr>
          <td>{{ t('calc.quoted', { quantity: result.quantity, price: result.unit_price }) }}</td>
          <td />
          <td />
          <td class="num" data-testid="base">{{ $money(result.base_amount) }}</td>
        </tr>
        <tr v-if="Number(result.discount_amount) !== 0">
          <td>{{ t('calc.discount') }}</td>
          <td />
          <td />
          <td class="num">−{{ $money(result.discount_amount) }}</td>
        </tr>
        <tr>
          <td>{{ t('calc.netRevenue') }} <small v-if="Number(result.rounding_adjustment) !== 0" class="text-muted-foreground" data-testid="adjustment">{{ t('calc.rounding', { amount: $money(result.rounding_adjustment) }) }}</small></td>
          <td />
          <td />
          <td class="num" data-testid="net">{{ $money(result.net_amount) }}</td>
        </tr>
        <tr v-for="c in result.service_charges" :key="`s${c.rule_id}`" data-testid="service-line">
          <td>{{ c.name }}</td>
          <td class="num">{{ Number(c.rate) }}%</td>
          <td class="num">{{ $money(c.base_amount) }}</td>
          <td class="num">{{ $money(c.amount) }}</td>
        </tr>
        <tr v-for="c in result.taxes" :key="`t${c.rule_id}`" data-testid="tax-line">
          <td>{{ c.name }}<small v-if="c.tax_on_service" class="text-muted-foreground"> {{ t('calc.inclService') }}</small></td>
          <td class="num">{{ Number(c.rate) }}%</td>
          <td class="num">{{ $money(c.base_amount) }}</td>
          <td class="num">{{ $money(c.amount) }}</td>
        </tr>
        <tr class="border-t-2 border-border">
          <td><b>{{ t('calc.total') }}</b> <small class="text-muted-foreground">{{ result.price_mode === 'INCLUSIVE' ? t('calc.priceInclusive') : t('calc.priceExclusive') }}</small></td>
          <td />
          <td />
          <td class="num" data-testid="total"><b>{{ $money(result.total_amount) }}</b></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
