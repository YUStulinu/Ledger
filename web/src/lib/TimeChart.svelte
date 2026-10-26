<script lang="ts">
  import { compact, day, money, ticks } from "./format";

  // A line over dates, with an optional shaded band (the forecast's 10–90% range), a second line
  // (what actually happened) and a horizontal threshold.
  type Line = { name: string; values: (number | null)[]; cls: string };
  let {
    dates,
    lines,
    band,
    threshold,
    height = 260,
  }: {
    dates: string[];
    lines: Line[];
    band?: { low: number[]; high: number[]; offset?: number };
    threshold?: number;
    height?: number;
  } = $props();

  let width = $state(720);
  let hover = $state<number | null>(null);
  const pad = { top: 12, right: 12, bottom: 28, left: 52 };

  const all = $derived([
    ...lines.flatMap((l) => l.values.filter((v): v is number => v !== null)),
    ...(band ? [...band.low, ...band.high] : []),
    ...(threshold ? [threshold] : []),
  ]);
  const lo0 = $derived(Math.min(0, ...all));
  const hi0 = $derived(Math.max(1, ...all));
  const yTicks = $derived(ticks(lo0, hi0));
  const lo = $derived(Math.min(lo0, yTicks[0] ?? lo0));
  const hi = $derived(Math.max(hi0, yTicks[yTicks.length - 1] ?? hi0));
  const plotW = $derived(width - pad.left - pad.right);
  const plotH = $derived(height - pad.top - pad.bottom);
  const x = (i: number) => pad.left + (dates.length > 1 ? (i / (dates.length - 1)) * plotW : plotW / 2);
  const y = (v: number) => pad.top + plotH - ((v - lo) / (hi - lo || 1)) * plotH;

  function path(values: (number | null)[]) {
    let d = "";
    let pen = false;
    values.forEach((v, i) => {
      if (v === null) {
        pen = false;
        return;
      }
      d += `${pen ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
      pen = true;
    });
    return d;
  }

  const bandPath = $derived.by(() => {
    if (!band) return "";
    const off = band.offset ?? 0;
    const up = band.high.map((v, i) => `${i ? "L" : "M"}${x(i + off).toFixed(1)},${y(v).toFixed(1)}`).join("");
    const down = band.low
      .map((v, i) => [i, v] as const)
      .reverse()
      .map(([i, v]) => `L${x(i + off).toFixed(1)},${y(v).toFixed(1)}`)
      .join("");
    return up + down + "Z";
  });

  // About one label per 90px, on the first of a month when the range is long.
  const labels = $derived.by(() => {
    const n = Math.max(2, Math.floor(plotW / 90));
    const step = Math.max(1, Math.round(dates.length / n));
    return dates.map((d, i) => ({ d, i })).filter(({ i }) => i % step === 0);
  });

  function move(e: MouseEvent) {
    const rect = (e.currentTarget as SVGElement).getBoundingClientRect();
    const px = e.clientX - rect.left;
    const i = Math.round(((px - pad.left) / plotW) * (dates.length - 1));
    hover = i >= 0 && i < dates.length ? i : null;
  }
</script>

<div class="chart" bind:clientWidth={width}>
  <svg {width} {height} role="img" aria-label="Balance over time" onmousemove={move} onmouseleave={() => (hover = null)}>
    {#each yTicks as t}
      <line class="grid" x1={pad.left} x2={width - pad.right} y1={y(t)} y2={y(t)} />
      <text class="axis" x={pad.left - 6} y={y(t) + 4} text-anchor="end">{compact(t)}</text>
    {/each}
    {#each labels as l}
      <text class="axis" x={x(l.i)} y={height - 8} text-anchor="middle">{day(l.d).replace(/ 20(\d\d)$/, " $1")}</text>
    {/each}
    {#if bandPath}
      <path d={bandPath} class="band" />
    {/if}
    {#if threshold}
      <line class="threshold" x1={pad.left} x2={width - pad.right} y1={y(threshold)} y2={y(threshold)} />
    {/if}
    {#each lines as l}
      <path d={path(l.values)} class="line {l.cls}" />
    {/each}
    {#if hover !== null}
      <line class="cursor" x1={x(hover)} x2={x(hover)} y1={pad.top} y2={pad.top + plotH} />
    {/if}
  </svg>
  {#if hover !== null}
    {@const i = hover}
    <div class="tip" style:left="{Math.min(x(i) + 12, width - 200)}px">
      <strong>{day(dates[i])}</strong>
      {#each lines as l}
        {#if l.values[i] !== null && l.values[i] !== undefined}
          <div class="row"><span>{l.name}</span><span>{money(l.values[i] as number)}</span></div>
        {/if}
      {/each}
      {#if band && i - (band.offset ?? 0) >= 0 && i - (band.offset ?? 0) < band.low.length}
        <div class="row muted">
          <span>10–90%</span><span>{compact(band.low[i - (band.offset ?? 0)])} – {compact(band.high[i - (band.offset ?? 0)])}</span>
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .chart {
    position: relative;
    width: 100%;
  }
  svg {
    display: block;
  }
  .line {
    fill: none;
    stroke-width: 2;
    stroke-linejoin: round;
  }
  :global(.line.main) {
    stroke: var(--accent);
  }
  :global(.line.history) {
    stroke: var(--text);
  }
  :global(.line.actual) {
    stroke: var(--warn);
    stroke-dasharray: 5 3;
  }
  .band {
    fill: var(--accent);
    opacity: 0.16;
  }
  .threshold {
    stroke: var(--bad);
    stroke-dasharray: 3 3;
  }
  .cursor {
    stroke: var(--muted);
    stroke-width: 1;
  }
  .tip {
    top: 8px;
    width: 190px;
  }
</style>
