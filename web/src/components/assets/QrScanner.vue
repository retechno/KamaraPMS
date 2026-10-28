<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { t } from '@/i18n'

/**
 * The camera of the phone reading a QR code (docs/architecture/37-fixed-assets.md 4.10, Q12). The browser's BarcodeDetector reads it where it exists; elsewhere (Safari) the frames are read by jsQR, which is loaded only then.
 * The camera is switched off when `active` turns false and when the component goes away. `decoded` is emitted once for each new text (the same code is not repeated while it stays in front of the lens).
 */
const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{ decoded: [text: string]; failed: [reason: 'denied' | 'unavailable'] }>()

interface Detector { detect(source: CanvasImageSource): Promise<{ rawValue: string }[]> }
type DetectorCtor = new (opts: { formats: string[] }) => Detector

const video = ref<HTMLVideoElement | null>(null)
const problem = ref<'denied' | 'unavailable' | null>(null)
let stream: MediaStream | null = null
let timer: ReturnType<typeof setInterval> | null = null
let last = ''
let lastAt = 0
let busy = false

function stop(): void {
  if (timer !== null) clearInterval(timer)
  timer = null
  for (const track of stream?.getTracks() ?? []) track.stop()
  stream = null
  if (video.value) video.value.srcObject = null
}

function seen(text: string): void {
  const now = Date.now()
  if (text === last && now - lastAt < 2500) return
  last = text
  lastAt = now
  emit('decoded', text)
}

async function start(): Promise<void> {
  problem.value = null
  const media = typeof navigator === 'undefined' ? undefined : navigator.mediaDevices
  if (!media?.getUserMedia) {
    problem.value = 'unavailable'
    emit('failed', 'unavailable')
    return
  }
  try {
    stream = await media.getUserMedia({ video: { facingMode: 'environment' }, audio: false })
  } catch (e) {
    problem.value = e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'SecurityError') ? 'denied' : 'unavailable'
    emit('failed', problem.value)
    return
  }
  if (!props.active) {
    stop()
    return
  }
  const el = video.value
  if (!el) return
  el.srcObject = stream
  await el.play().catch(() => undefined)
  const Native = (window as unknown as { BarcodeDetector?: DetectorCtor }).BarcodeDetector
  const detector = Native ? new Native({ formats: ['qr_code'] }) : null
  const jsQR = detector ? null : (await import('jsqr')).default
  const canvas = document.createElement('canvas')
  timer = setInterval(async () => {
    if (busy || !el.videoWidth) return
    busy = true
    try {
      if (detector) {
        const found = await detector.detect(el)
        if (found[0]?.rawValue) seen(found[0].rawValue)
      } else if (jsQR) {
        canvas.width = el.videoWidth
        canvas.height = el.videoHeight
        const ctx = canvas.getContext('2d', { willReadFrequently: true })
        if (ctx) {
          ctx.drawImage(el, 0, 0)
          const img = ctx.getImageData(0, 0, canvas.width, canvas.height)
          const code = jsQR(img.data, img.width, img.height)
          if (code?.data) seen(code.data)
        }
      }
    } catch {
      // a frame that cannot be read is skipped; the next one is tried
    } finally {
      busy = false
    }
  }, 250)
}

watch(() => props.active, (on) => { if (on) void start(); else stop() }, { immediate: true })
onBeforeUnmount(stop)
</script>

<template>
  <div class="relative overflow-hidden rounded-lg bg-black" data-testid="qr-scanner">
    <video v-show="active && !problem" ref="video" class="aspect-[4/3] w-full object-cover" playsinline muted />
    <p v-if="problem" class="m-0 p-4 text-sm text-white" role="alert" data-testid="qr-problem">{{ t(`assets.audit.camera.${problem}`) }}</p>
  </div>
</template>
