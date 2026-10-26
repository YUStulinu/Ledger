<script lang="ts">
  import { api, type Overview, type Status } from "../lib/api";
  import { color } from "../lib/colors";
  import { money, monthLabel } from "../lib/format";
  import StackedBars from "../lib/StackedBars.svelte";
  import TimeChart from "../lib/TimeChart.svelte";

  let { status }: { status: Status | null } = $props();

  let data = $state<Overview | null>(null);
  let error = $state("");

  $effect(() => {
    api.overview().then((d) => (data = d), (e) => (error = e.message));
  });

  const TOP = 8;
  // The last 12 months, the 8 largest categories over them and "Other" for the rest.
  const chart = $derived.by(() => {
    if (!data?.monthly) return null;
    const months = [...new Set(data.monthly.map((r) => r.month))].sort().slice(-12);
    const totals = new Map<string, number>();
    for (const r of data.monthly) if (months.includes(r.month)) totals.set(r.category, (totals.get(r.category) ?? 0) + r.amount);
    const top = [...totals.entries()].sort((a, b) => b[1] - a[1]).slice(0, TOP).map(([c]) => c);
    const names = [...top, "Other"];
    const values = names.map(() => months.map(() => 0));
    for (const r of data.monthly) {
      const m = months.indexOf(r.month);
      if (m < 0) continue;
      const c = top.indexOf(r.category);
      values[c < 0 ? TOP : c][m] += r.amount;
    }
    const income = months.map((m) => data!.income?.find((r) => r.month === m)?.amount ?? 0);
    return { months, series: names.map((name, i) => ({ name, values: values[i] })), income };
  });

  const kpi = $derived.by(() => {
    if (!chart || chart.months.length === 0) return null;
    const n = chart.months.length;
    const spent = (i: number) => chart.series.reduce((s, x) => s + (x.values[i] ?? 0), 0);
    const hist = data?.balance_history ?? [];
    return {
      net: hist.length ? hist[hist.length - 1].ron : 0,
      month: chart.months[n - 1],
      spent: spent(n - 1),
      prev: n > 1 ? spent(n - 2) : null,
      income: chart.income[n - 1],
    };
  });
</script>

{#if error}
  <p class="error">{error}</p>
{:else if !data}
  <p class="muted">Loading…</p>
{:else if !data.balances?.length}
  <div class="card empty">
    <h2>No statements yet</h2>
    <p>Import CSV or PDF statements from BT, ING or Revolut on the <a href="#/import">Import</a> page.</p>
  </div>
{:else}
  {#if kpi}
    <div class="kpis">
      <div class="card kpi">
        <span class="label">Net worth (RON)</span>
        <span class="value">{money(kpi.net)}</span>
        <span class="muted">all accounts, EUR converted at BNR rates</span>
      </div>
      <div class="card kpi">
        <span class="label">Spent in {monthLabel(kpi.month)}</span>
        <span class="value">{money(kpi.spent)}</span>
        {#if kpi.prev !== null}
          {@const d = kpi.spent - kpi.prev}
          <span class={d > 0 ? "bad" : "good"}>{d > 0 ? "▲" : "▼"} {money(Math.abs(d))} vs the month before</span>
        {/if}
      </div>
      <div class="card kpi">
        <span class="label">Income in {monthLabel(kpi.month)}</span>
        <span class="value">{money(kpi.income)}</span>
        <span class="muted">saved {money(kpi.income - kpi.spent)}</span>
      </div>
      <div class="card kpi">
        <span class="label">To review</span>
        <span class="value">{status?.uncategorized ?? 0}</span>
        <span class="muted"
          >{#if status?.uncategorized}<a href="#/transactions?uncategorized=1">uncategorized transactions</a
            >{:else}every transaction has a category{/if}</span
        >
      </div>
    </div>
  {/if}

  <div class="grid2">
    <section class="card wide">
      <header>
        <h2>Spending per month</h2>
        <span class="muted">stacked by category · dashed line: income</span>
      </header>
      {#if chart}
        <StackedBars months={chart.months} series={chart.series} income={chart.income} />
        <div class="legend">
          {#each chart.series as s}
            <span><i style:background={color(s.name)}></i>{s.name}</span>
          {/each}
        </div>
      {/if}
    </section>

    <section class="card">
      <header><h2>Accounts</h2></header>
      <table>
        <tbody>
          {#each data.balances as b}
            <tr>
              <td>{b.bank}<div class="muted small mono">{b.account}</div></td>
              <td class="num">{money(b.balance, b.currency)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </section>

    <section class="card wide">
      <header><h2>Balance history</h2><span class="muted">all accounts, in RON</span></header>
      {#if data.balance_history?.length}
        <TimeChart
          dates={data.balance_history.map((d) => d.date)}
          lines={[{ name: "Balance", values: data.balance_history.map((d) => d.ron), cls: "main" }]}
          height={220}
        />
      {/if}
    </section>

    <section class="card">
      <header><h2>Top merchants</h2><span class="muted">last 90 days</span></header>
      <table>
        <tbody>
          {#each data.top_merchants ?? [] as m}
            <tr>
              <td>
                <a href="#/transactions?q={encodeURIComponent(m.merchant)}">{m.merchant}</a>
                <div class="small"><span class="dot" style:background={color(m.category)}></span>{m.category} · {m.count}×</div>
              </td>
              <td class="num">{money(m.amount)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </section>
  </div>
{/if}

<style>
  .kpis {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 16px;
    margin-bottom: 16px;
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
  .grid2 {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
    gap: 16px;
  }
  @media (max-width: 900px) {
    .grid2 {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 14px;
    margin-top: 8px;
    font-size: 12.5px;
    color: var(--muted);
  }
  .legend i {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 2px;
    margin-right: 5px;
    vertical-align: -1px;
  }
  .empty {
    text-align: center;
    padding: 48px 16px;
  }
</style>
