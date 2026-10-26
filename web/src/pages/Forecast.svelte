<script lang="ts">
  import { api, type Forecast, type Status } from "../lib/api";
  import { day, money } from "../lib/format";
  import TimeChart from "../lib/TimeChart.svelte";

  let { status }: { status: Status | null } = $props();

  let days = $state(90);
  let threshold = $state(5000);
  let backtest = $state(false);
  let data = $state<Forecast | null>(null);
  let loading = $state(false);
  let error = $state("");

  // A backtest forecasts from `days` before the end of the data and compares with what happened.
  const from = $derived.by(() => {
    if (!backtest || !status?.data_end) return "";
    const d = new Date(status.data_end + "T00:00:00Z");
    d.setUTCDate(d.getUTCDate() - days);
    return d.toISOString().slice(0, 10);
  });

  let seq = 0;
  $effect(() => {
    const [n, t, f] = [days, threshold, from];
    const mine = ++seq;
    loading = true;
    const timer = setTimeout(() => {
      api.forecast(n, t || 0, f).then(
        (r) => {
          if (mine !== seq) return;
          data = r;
          error = "";
          loading = false;
        },
        (e) => {
          if (mine !== seq) return;
          error = e.message;
          loading = false;
        },
      );
    }, 250);
    return () => clearTimeout(timer);
  });

  const chart = $derived.by(() => {
    if (!data) return null;
    const hist = data.history ?? [];
    const h = hist.length;
    const dates = [...hist.map((p) => p.date), ...data.days];
    const pad = (n: number) => Array<number | null>(n).fill(null);
    // The median starts at the last known balance, so the lines join.
    const median = [...pad(Math.max(0, h - 1)), ...(h ? [data.start_balance] : []), ...data.p50];
    const lines = [
      { name: "Balance", values: [...hist.map((p) => p.ron), ...pad(data.days.length)], cls: "history" },
      { name: "Forecast (median)", values: median, cls: "main" },
    ];
    if (data.actual?.length) lines.push({ name: "What happened", values: [...pad(h), ...data.actual], cls: "actual" });
    return { dates, lines, band: { low: data.p10, high: data.p90, offset: h } };
  });
</script>

<section class="card">
  <div class="controls">
    <label
      >Horizon
      <select bind:value={days}>
        <option value={30}>1 month</option>
        <option value={60}>2 months</option>
        <option value={90}>3 months</option>
      </select>
    </label>
    <label>Warn below <input type="number" min="0" step="500" bind:value={threshold} /> RON</label>
    <label class="check"><input type="checkbox" bind:checked={backtest} /> Backtest: forecast from {days} days before the data ends</label>
    {#if loading}<span class="muted small">Simulating…</span>{/if}
  </div>
  {#if error}<p class="error">{error}</p>{/if}
  {#if chart && data}
    <TimeChart dates={chart.dates} lines={chart.lines} band={chart.band} threshold={threshold || undefined} height={320} />
    <p class="muted small">
      Shaded: the range the balance stays in on 80% of 2000 simulated paths (10th to 90th percentile). The simulation replays
      {data.scheduled} scheduled flows (salary, rent, subscriptions) on their dates and resamples everyday spending, by weekday, from
      the last 26 weeks.
    </p>
  {/if}
</section>

{#if data}
  <div class="kpis">
    <div class="card kpi">
      <span class="label">In {days} days ({day(data.days[data.days.length - 1])})</span>
      <span class="value">{money(data.end.p50)}</span>
      <span class="muted">likely between {money(data.end.p10)} and {money(data.end.p90)}</span>
    </div>
    <div class="card kpi">
      <span class="label">Lowest point</span>
      <span class="value">{money(data.lowest.p50)}</span>
      <span class="muted">around {day(data.lowest.date)}; in a bad case {money(data.lowest.p10)}</span>
    </div>
    <div class="card kpi">
      <span class="label">Chance to fall below {money(data.threshold)}</span>
      <span class="value" class:bad={data.chance_below_threshold > 0.2}>{Math.round(data.chance_below_threshold * 100)}%</span>
      <span class="muted">at any point in the period</span>
    </div>
    <div class="card kpi">
      <span class="label">Everyday spending</span>
      <span class="value">{money(Math.abs(data.everyday_spending_per_month))}</span>
      <span class="muted">a month, besides the scheduled flows</span>
    </div>
    {#if data.actual?.length}
      <div class="card kpi">
        <span class="label">Backtest</span>
        <span class="value">{Math.round((data.coverage_10_90 ?? 0) * 100)}%</span>
        <span class="muted"
          >of days inside the shaded range; off by {money(Math.abs(data.end_error ?? 0))} at the end, {money(data.mean_abs_error ?? 0)} on an average day</span
        >
      </div>
    {/if}
  </div>
{/if}

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: 16px;
    align-items: center;
    margin-bottom: 12px;
    font-size: 14px;
  }
  .controls label {
    display: flex;
    gap: 6px;
    align-items: center;
  }
  .controls input[type="number"] {
    width: 100px;
  }
  .kpis {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 16px;
    margin-top: 16px;
  }
  .kpi {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .kpi .label {
    font-size: 13px;
    color: var(--muted);
  }
  .kpi .value {
    font-size: 24px;
    font-weight: 650;
    font-variant-numeric: tabular-nums;
  }
  .kpi span:last-child {
    font-size: 13px;
  }
</style>
