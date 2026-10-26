<script lang="ts">
  import { color } from "./colors";
  import { compact, money, monthLabel, ticks } from "./format";

  // Spending per month, stacked by category, with income as a line on top.
  let {
    months,
    series,
    income = [],
    height = 260,
  }: { months: string[]; series: { name: string; values: number[] }[]; income?: number[]; height?: number } = $props();

  let width = $state(720);
  let hover = $state<number | null>(null);

  const pad = { top: 12, right: 12, bottom: 28, left: 48 };
  const totals = $derived(months.map((_, i) => series.reduce((s, x) => s + Math.max(0, x.values[i] ?? 0), 0)));
  const max = $derived(Math.max(1, ...totals, ...income));
  const yTicks = $derived(ticks(0, max));
  const top = $derived(Math.max(max, yTicks[yTicks.length - 1] ?? max));
  const plotW = $derived(width - pad.left - pad.right);
  const plotH = $derived(height - pad.top - pad.bottom);
  const band = $derived(plotW / Math.max(1, months.length));
  const barW = $derived(Math.max(4, Math.min(36, band * 0.68)));
  const y = (v: number) => pad.top + plotH - (v / top) * plotH;
  const x = (i: number) => pad.left + band * i + band / 2;

  function stack(i: number) {
    let acc = 0;
    return series
      .map((s) => {
        const v = Math.max(0, s.values[i] ?? 0);
        const seg = { name: s.name, v, y0: acc, y1: acc + v };
        acc += v;
        return seg;
      })
      .filter((s) => s.v > 0);
  }

  const labelEvery = $derived(Math.ceil(months.length / Math.max(1, Math.floor(plotW / 56))));
  const incomePath = $derived(
    income.length ? income.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ") : "",
  );
</script>

<div class="chart" bind:clientWidth={width}>
  <svg {width} {height} role="img" aria-label="Monthly spending by category">
    {#each yTicks as t}
      <line class="grid" x1={pad.left} x2={width - pad.right} y1={y(t)} y2={y(t)} />
      <text class="axis" x={pad.left - 6} y={y(t) + 4} text-anchor="end">{compact(t)}</text>
    {/each}
    {#each months as m, i}
      <g
        role="presentation"
        onmouseenter={() => (hover = i)}
        onmouseleave={() => (hover = null)}
        opacity={hover === null || hover === i ? 1 : 0.55}
      >
        <rect x={x(i) - band / 2} y={pad.top} width={band} height={plotH} fill="transparent" />
        {#each stack(i) as s}
          <rect x={x(i) - barW / 2} y={y(s.y1)} width={barW} height={Math.max(0, y(s.y0) - y(s.y1))} fill={color(s.name)} />
        {/each}
      </g>
      {#if i % labelEvery === 0}
        <text class="axis" x={x(i)} y={height - 8} text-anchor="middle">{monthLabel(m)}</text>
      {/if}
    {/each}
    {#if incomePath}
      <path d={incomePath} class="income" />
      {#each income as v, i}
        <circle cx={x(i)} cy={y(v)} r="2.5" class="income-dot" />
      {/each}
    {/if}
  </svg>
  {#if hover !== null}
    {@const i = hover}
    <div class="tip" style:left="{Math.min(x(i) + 12, width - 220)}px">
      <strong>{monthLabel(months[i])}</strong>
      <div class="row total"><span>Spent</span><span>{money(totals[i])}</span></div>
      {#if income.length}
        <div class="row"><span>Income</span><span>{money(income[i] ?? 0)}</span></div>
      {/if}
      {#each stack(i).toReversed().slice(0, 8) as s}
        <div class="row"><span><i style:background={color(s.name)}></i>{s.name}</span><span>{money(s.v)}</span></div>
      {/each}
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
  .income {
    fill: none;
    stroke: var(--text);
    stroke-width: 1.5;
    stroke-dasharray: 4 3;
  }
  .income-dot {
    fill: var(--text);
  }
  .tip {
    top: 8px;
    width: 210px;
  }
</style>
