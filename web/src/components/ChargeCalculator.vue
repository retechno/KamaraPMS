<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CalculateChargeRequest, ChargeCalculation, PriceMode } from '@/api/types'
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
  <section class="calc" data-testid="calculator">
    <h2>Calculator</h2>
    <p class="muted">Previews the calculation with the saved rules. Nothing is posted.</p>
    <p v-if="error && !error.fieldErrors.length" class="alert" role="alert" data-testid="calc-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <div class="form-grid" @keydown.enter.prevent="calculate">
      <label class="field">
        <span>Quantity</span>
        <input v-model="quantity" name="quantity" inputmode="decimal" :aria-invalid="!!fieldError('quantity')" />
        <small v-if="fieldError('quantity')" class="error-text">{{ fieldError('quantity') }}</small>
      </label>
      <label class="field">
        <span>Unit price {{ currency }}</span>
        <input v-model="unitPrice" name="unit_price" inputmode="decimal" :aria-invalid="!!fieldError('unit_price')" />
        <small class="hint">Negative for a credit.</small>
        <small v-if="fieldError('unit_price')" class="error-text">{{ fieldError('unit_price') }}</small>
      </label>
      <label class="field">
        <span>Discount</span>
        <input v-model="discount" name="discount_amount" inputmode="decimal" :aria-invalid="!!fieldError('discount_amount')" />
        <small v-if="fieldError('discount_amount')" class="error-text">{{ fieldError('discount_amount') }}</small>
      </label>
      <label class="field">
        <span>Price is</span>
        <select v-model="modeOverride" name="price_mode">
          <option value="">As the code ({{ priceMode.toLowerCase() }})</option>
          <option value="EXCLUSIVE">Exclusive</option>
          <option value="INCLUSIVE">Inclusive</option>
        </select>
      </label>
      <div class="actions">
        <button type="button" :disabled="busy" data-testid="calculate" @click="calculate">Calculate</button>
      </div>
    </div>

    <table v-if="result" class="list result" data-testid="result">
      <thead>
        <tr>
          <th>Line</th>
          <th class="num">Rate</th>
          <th class="num">On</th>
          <th class="num">Amount</th>
        </tr>
      </thead>
      <tbody>
        <tr>
          <td>Quoted amount ({{ result.quantity }} × {{ result.unit_price }})</td>
          <td />
          <td />
          <td class="num" data-testid="base">{{ result.base_amount }}</td>
        </tr>
        <tr v-if="Number(result.discount_amount) !== 0">
          <td>Discount</td>
          <td />
          <td />
          <td class="num">−{{ result.discount_amount }}</td>
        </tr>
        <tr>
          <td>Net revenue <small v-if="Number(result.rounding_adjustment) !== 0" class="muted" data-testid="adjustment">(includes rounding {{ result.rounding_adjustment }})</small></td>
          <td />
          <td />
          <td class="num" data-testid="net">{{ result.net_amount }}</td>
        </tr>
        <tr v-for="c in result.service_charges" :key="`s${c.rule_id}`" data-testid="service-line">
          <td>{{ c.name }}</td>
          <td class="num">{{ Number(c.rate) }}%</td>
          <td class="num">{{ c.base_amount }}</td>
          <td class="num">{{ c.amount }}</td>
        </tr>
        <tr v-for="c in result.taxes" :key="`t${c.rule_id}`" data-testid="tax-line">
          <td>{{ c.name }}<small v-if="c.tax_on_service" class="muted"> (incl. service)</small></td>
          <td class="num">{{ Number(c.rate) }}%</td>
          <td class="num">{{ c.base_amount }}</td>
          <td class="num">{{ c.amount }}</td>
        </tr>
        <tr class="total">
          <td><b>Total</b> <small class="muted">{{ result.price_mode.toLowerCase() }} price</small></td>
          <td />
          <td />
          <td class="num" data-testid="total"><b>{{ result.total_amount }}</b></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.calc {
  margin-top: 24px;
  border-top: 1px solid var(--border);
  padding-top: 16px;
}
.actions {
  display: flex;
  align-items: flex-end;
}
.result {
  margin-top: 16px;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.total td {
  border-top: 2px solid var(--border);
}
</style>
