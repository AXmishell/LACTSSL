<template>
  <Teleport to="body">
    <Transition name="loading-layer">
      <div
        v-if="loadingState.visible"
        class="loading-layer"
        role="status"
        aria-live="polite"
        aria-busy="true"
        :aria-label="loadingState.text || t('common.loading')"
      >
        <div class="loading-card">
          <div class="loading-mark" aria-hidden="true">
            <span class="loading-ring"></span>
            <span class="loading-ring loading-ring-inner"></span>
            <img src="/logo.svg" alt="" />
          </div>
          <div class="loading-copy">
            <div class="loading-title">
              {{ loadingState.text || t('common.loading') }}
              <span class="loading-dots" aria-hidden="true"><i></i><i></i><i></i></span>
            </div>
            <div class="loading-sub">{{ t('common.loadingSub') }}</div>
          </div>
          <div class="loading-track" aria-hidden="true"><span></span></div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { loadingState } from '../loading'

const { t } = useI18n()
</script>

<style scoped>
.loading-layer {
  position: fixed;
  inset: 0;
  z-index: 2600;
  display: grid;
  place-items: center;
  padding: 24px;
  cursor: progress;
  background:
    radial-gradient(circle at 50% 38%, rgba(255, 255, 255, 0.92), rgba(245, 248, 255, 0.88) 55%, rgba(237, 242, 251, 0.94));
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
}

.loading-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 18px;
  min-width: 212px;
  padding: 30px 34px 26px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  background: rgba(255, 255, 255, 0.92);
  box-shadow:
    0 30px 70px -40px rgba(15, 23, 42, 0.45),
    0 2px 6px rgba(15, 23, 42, 0.04);
}

.loading-mark {
  position: relative;
  width: 76px;
  height: 76px;
  display: grid;
  place-items: center;
}

.loading-mark img {
  position: relative;
  z-index: 1;
  width: 40px;
  height: 40px;
  filter: drop-shadow(0 8px 16px rgba(37, 99, 235, 0.28));
  animation: loading-breathe 1.8s ease-in-out infinite;
}

.loading-ring {
  position: absolute;
  inset: 0;
  border: 2px solid #e5ecf8;
  border-top-color: #2563eb;
  border-right-color: #60a5fa;
  animation: loading-spin 1.25s linear infinite;
}

.loading-ring-inner {
  inset: 11px;
  border: 1px solid #eef3fb;
  border-bottom-color: #a7f3d0;
  border-left-color: #d1fae5;
  animation: none;
}

.loading-copy {
  text-align: center;
}

.loading-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #0f172a;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.01em;
}

.loading-dots {
  display: inline-flex;
  gap: 3px;
  padding-top: 3px;
}

.loading-dots i {
  width: 4px;
  height: 4px;
  background: #2563eb;
  animation: loading-dot 1.1s ease-in-out infinite;
}

.loading-dots i:nth-child(2) {
  animation-delay: 0.15s;
}

.loading-dots i:nth-child(3) {
  animation-delay: 0.3s;
}

.loading-sub {
  margin-top: 6px;
  color: #94a3b8;
  font-size: 12px;
}

.loading-track {
  position: relative;
  width: 164px;
  height: 3px;
  background: #edf2f9;
  overflow: hidden;
}

.loading-track span {
  position: absolute;
  inset: 0 auto 0 0;
  width: 42%;
  background: linear-gradient(90deg, rgba(37, 99, 235, 0), #2563eb 45%, #60a5fa);
  animation: loading-slide 1.25s cubic-bezier(0.45, 0.05, 0.55, 0.95) infinite;
}

.loading-layer-enter-active,
.loading-layer-leave-active {
  transition: opacity 0.2s ease;
}

.loading-layer-enter-active .loading-card,
.loading-layer-leave-active .loading-card {
  transition: transform 0.24s cubic-bezier(0.2, 0.8, 0.3, 1), opacity 0.2s ease;
}

.loading-layer-enter-from,
.loading-layer-leave-to {
  opacity: 0;
}

.loading-layer-enter-from .loading-card,
.loading-layer-leave-to .loading-card {
  opacity: 0;
  transform: translateY(6px) scale(0.96);
}

@keyframes loading-spin {
  to {
    transform: rotate(360deg);
  }
}

@keyframes loading-breathe {
  0%,
  100% {
    opacity: 0.85;
    transform: scale(0.94);
  }
  50% {
    opacity: 1;
    transform: scale(1.04);
  }
}

@keyframes loading-dot {
  0%,
  100% {
    opacity: 0.35;
    transform: translateY(0);
  }
  40% {
    opacity: 1;
    transform: translateY(-3px);
  }
}

@keyframes loading-slide {
  0% {
    transform: translateX(-110%);
  }
  100% {
    transform: translateX(260%);
  }
}

@media (max-width: 480px) {
  .loading-card {
    min-width: 0;
    width: 100%;
    max-width: 240px;
    padding: 26px 24px 22px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .loading-mark img {
    animation: none;
  }

  .loading-ring {
    animation-duration: 2.4s;
  }

  .loading-dots i {
    animation-duration: 2.2s;
  }

  .loading-track span {
    animation-duration: 2.8s;
  }

  .loading-layer-enter-active,
  .loading-layer-leave-active,
  .loading-layer-enter-active .loading-card,
  .loading-layer-leave-active .loading-card {
    transition-duration: 0.01ms;
  }
}
</style>
