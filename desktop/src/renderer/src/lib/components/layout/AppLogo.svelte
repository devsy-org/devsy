<script lang="ts">
let { class: className = "" }: { class?: string } = $props()
</script>

<svg
  viewBox="0 0 1024 1024"
  xmlns="http://www.w3.org/2000/svg"
  class={className}
  aria-label="Devsy"
  role="img"
>
  <rect x="0" y="0" width="1024" height="1024" rx="200" ry="200" fill="#7C5CFF" />
  <rect x="778" y="144" width="56" height="736" rx="12" fill="#FFFFFF" />
  <rect x="778" y="496" width="24" height="192" fill="#FFFFFF" />
  <circle cx="506" cy="592" r="288" fill="none" stroke="#FFFFFF" stroke-width="56" />
</svg>
