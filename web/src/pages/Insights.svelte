<script lang="ts">
  import { api, type Alert, type Recurring } from "../lib/api";
  import { color } from "../lib/colors";
  import { day, money } from "../lib/format";

  let recurring = $state<Recurring[]>([]);
  let alerts = $state<Alert[]>([]);
  let error = $state("");
  let showEnded = $state(false);

  $effect(() => {
    Promise.all([api.recurring(), api.alerts()]).then(
      ([r, a]) => {
        recurring = r ?? [];
        alerts = a ?? [];
      },
      (e) => (error = e.message),
    );
  });

  const costs = $derived(recurring.filter((r) => !r.income && (r.active || showEnded)));
  const yearly = $derived(recurring.filter((r) => !r.income && r.active).reduce((s, r) => s - r.yearly_ron, 0));
  const alertTitle: Record<Alert["kind"], string> = {
    duplicate: "Possible double charge",
    unusual_amount: "Unusual amount",
    new_fee: "New fee",
  };
</script>

{#if error}<p class="error">{error}</p>{/if}

<section class="card">
  <header>
    <h2>Alerts</h2>
    <span class="muted">{alerts.length || "none"}</span>
  </header>
  {#if alerts.length === 0}
    <p class="muted">Nothing unusual: no double charges, unusual amounts or new fees.</p>
  {/if}
  <div class="alerts">
    {#each alerts as a}
      <div class="alert alert-{a.kind}">
        <div class="alert-head">
          <strong>{alertTitle[a.kind]}</strong>
          <span class="muted small">{day(a.transaction.date)} · {a.transaction.account}</span>
        </div>
        <p>{a.message}</p>
        <div class="small">
          <a href="#/transactions?q={encodeURIComponent(a.transaction.merchant)}">{a.transaction.merchant}</a>
          · {money(a.transaction.amount, a.transaction.currency)}
        </div>
      </div>
    {/each}
  </div>
</section>

<section class="card">
  <header>
    <h2>Subscriptions and regular payments</h2>
    <span class="muted">{money(yearly)} a year</span>
  </header>
  <label class="check small"><input type="checkbox" bind:checked={showEnded} /> Show the ones that ended</label>
  <div class="scroll">
    <table>
      <thead>
        <tr><th>Merchant</th><th>Every</th><th class="num">Amount</th><th class="num">A year</th><th>Next</th><th>Since</th></tr>
      </thead>
      <tbody>
        {#each costs as r}
          <tr class:ended={!r.active}>
            <td>
              <a href="#/transactions?q={encodeURIComponent(r.merchant)}">{r.merchant}</a>
              <div class="small"><span class="dot" style:background={color(r.category)}></span>{r.category}</div>
            </td>
            <td>{r.cadence.replace("ly", "")}</td>
            <td class="num nowrap">
              {money(-r.amount, r.currency)}
              {#if r.previous_amount && r.changed_on}
                <div class="small {Math.abs(r.amount) > Math.abs(r.previous_amount) ? 'bad' : 'good'}">
                  was {money(-r.previous_amount, r.currency)} before {day(r.changed_on)}
                </div>
              {/if}
            </td>
            <td class="num nowrap">{money(-r.yearly_ron)}</td>
            <td class="nowrap">{r.active ? day(r.next) : "ended"}</td>
            <td class="nowrap muted">{day(r.first)}{#if r.new}<span class="pill new">new</span>{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<section class="card">
  <header><h2>Regular income</h2></header>
  <table>
    <tbody>
      {#each recurring.filter((r) => r.income && r.active) as r}
        <tr>
          <td>{r.merchant}<div class="small muted">{r.category} · every {r.cadence.replace("ly", "")}</div></td>
          <td class="num good">{money(r.amount, r.currency)}</td>
          <td class="nowrap">next {day(r.next)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</section>

<style>
  section + section {
    margin-top: 16px;
  }
  .alerts {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 12px;
  }
  .alert {
    border: 1px solid var(--border);
    border-left: 4px solid var(--warn);
    border-radius: 8px;
    padding: 10px 12px;
  }
  .alert-duplicate {
    border-left-color: var(--bad);
  }
  .alert p {
    margin: 6px 0;
    font-size: 14px;
  }
  .alert-head {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
  }
  .ended {
    opacity: 0.55;
  }
  .check {
    display: flex;
    gap: 6px;
    align-items: center;
    margin-bottom: 8px;
  }
  .scroll {
    overflow-x: auto;
  }
  .new {
    margin-left: 6px;
    color: var(--accent);
  }
</style>
