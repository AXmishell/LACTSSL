<template>
  <Teleport to="body">
    <div
      class="route-progress"
      :class="{ 'is-visible': routeProgress.visible }"
      :aria-hidden="!routeProgress.visible"
      role="progressbar"
      :aria-label="t('common.loading')"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="Math.round(routeProgress.value)"
    >
      <span class="route-progress-bar" :style="{ width: `${routeProgress.value}%` }"></span>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { routeProgress } from '../loading'

const { t } = useI18n()
</script>

<style scoped>
.route-progress {
  position: fixed;
  top: 0;
  right: 0;
  left: 0;
  z-index: 2550;
  height: 3px;
  overflow: hidden;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.22s ease;
}

.route-progress.is-visible {
  opacity: 1;
}

.route-progress-bar {
  display: block;
  height: 100%;
  border-radius: 0 2px 2px 0;
  background: linear-gradient(90deg, #93c5fd, #3b82f6 55%, #1d4ed8);
  box-shadow:
    0 0 8px rgba(37, 99, 235, 0.55),
    0 0 2px rgba(37, 99, 235, 0.9);
  transition: width 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  will-change: width;
}

@media (prefers-reduced-motion: reduce) {
  .route-progress,
  .route-progress-bar {
    transition-duration: 0.01ms;
  }
}
</style>
