<script lang="ts">
  import { api, type Category, type Tx } from "../lib/api";
  import { color } from "../lib/colors";
  import { day, money } from "../lib/format";

  let { params, categories, onchange }: { params: URLSearchParams; categories: Category[]; onchange: () => void } = $props();

  const PAGE = 100;
  let q = $state("");
  let category = $state("");
  let month = $state("");
  let uncategorized = $state(false);
  let offset = $state(0);

  let items = $state<Tx[]>([]);
  let total = $state(0);
  let loading = $state(false);
  let error = $state("");
  let notice = $state("");

  // Filters can come from a link (#/transactions?q=KAUFLAND).
  $effect(() => {
    q = params.get("q") ?? "";
    category = params.get("category") ?? "";
    month = params.get("month") ?? "";
    uncategorized = params.get("uncategorized") === "1";
    offset = 0;
  });

  let seq = 0;
  async function load() {
    const mine = ++seq;
    loading = true;
    try {
      const res = await api.transactions({ q, category, month, uncategorized: uncategorized ? 1 : undefined, limit: PAGE, offset });
      if (mine !== seq) return;
      items = res.items;
      total = res.total;
      error = "";
    } catch (e: any) {
      if (mine === seq) error = e.message;
    } finally {
      if (mine === seq) loading = false;
    }
  }

  let timer: ReturnType<typeof setTimeout>;
  $effect(() => {
    // Read every filter so the effect reruns when one changes; typing waits a moment.
    void [q, category, month, uncategorized, offset];
    clearTimeout(timer);
    timer = setTimeout(load, 200);
  });

  const assignable = $derived(categories.filter((c) => c.Assignable));
  const sources: Record<string, string> = {
    user: "set by you",
    rule: "from a rule",
    model: "learned from your corrections",
    llm: "guessed by the language model",
  };

  async function correct(t: Tx, value: string) {
    if (!value || value === t.category) return;
    try {
      const res = await api.correct(t.key, value);
      notice = `${t.merchant || "This merchant"} is now ${value} (${res.updated} transaction${res.updated === 1 ? "" : "s"}). Future imports follow it too.`;
      await load();
      onchange();
    } catch (e: any) {
      error = e.message;
    }
  }

  const correctable = (t: Tx) => (t.kind === "expense" || t.kind === "income") && t.key !== "";
</script>

<section class="card">
  <div class="filters">
    <input type="search" placeholder="Search merchant or description" bind:value={q} oninput={() => (offset = 0)} />
    <select bind:value={category} onchange={() => (offset = 0)}>
      <option value="">All categories</option>
      {#each categories as c}
        <option value={c.Name}>{c.Name}</option>
      {/each}
    </select>
    <input type="month" bind:value={month} onchange={() => (offset = 0)} />
    <label class="check"><input type="checkbox" bind:checked={uncategorized} onchange={() => (offset = 0)} /> Uncategorized only</label>
    <span class="muted count">{loading ? "Loading…" : `${total} transactions`}</span>
  </div>

  {#if notice}
    <p class="notice">{notice} <button class="link" onclick={() => (notice = "")}>Dismiss</button></p>
  {/if}
  {#if error}
    <p class="error">{error}</p>
  {/if}

  <div class="scroll">
    <table class="tx">
      <thead>
        <tr><th>Date</th><th>Merchant</th><th>Account</th><th>Category</th><th class="num">Amount</th></tr>
      </thead>
      <tbody>
        {#each items as t (t.id + t.key)}
          <tr>
            <td class="nowrap">{day(t.date)}</td>
            <td class="merchant">
              <div>{t.merchant || "—"}</div>
              <div class="muted small desc" title={t.description}>{t.description}</div>
            </td>
            <td class="nowrap">{t.bank} <span class="muted small">{t.currency}</span></td>
            <td class="nowrap">
              {#if correctable(t)}
                <span class="dot" style:background={color(t.category)}></span>
                <select
                  class="inline"
                  class:uncat={t.category === "Uncategorized"}
                  value={t.category}
                  title={sources[t.source] ?? ""}
                  onchange={(e) => correct(t, (e.currentTarget as HTMLSelectElement).value)}
                >
                  {#if t.category === "Uncategorized"}<option value="Uncategorized">Uncategorized</option>{/if}
                  {#each assignable as c}
                    <option value={c.Name}>{c.Name}</option>
                  {/each}
                </select>
                {#if t.source}<span class="src src-{t.source}" title={sources[t.source]}>{t.source}</span>{/if}
              {:else}
                <span class="pill">{t.kind === "transfer" ? "Transfer" : t.kind === "exchange" ? "Exchange" : t.category}</span>
              {/if}
            </td>
            <td class="num nowrap" class:good={t.amount > 0}>{money(t.amount, t.currency)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  {#if total > PAGE}
    <div class="pager">
      <button disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - PAGE))}>← Newer</button>
      <span class="muted">{offset + 1}–{Math.min(offset + PAGE, total)} of {total}</span>
      <button disabled={offset + PAGE >= total} onclick={() => (offset += PAGE)}>Older →</button>
    </div>
  {/if}
</section>

<style>
  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    align-items: center;
    margin-bottom: 12px;
  }
  .filters input[type="search"] {
    flex: 1 1 240px;
  }
  .check {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 14px;
  }
  .count {
    margin-left: auto;
    font-size: 13px;
  }
  .scroll {
    overflow-x: auto;
  }
  .tx {
    min-width: 720px;
  }
  .merchant {
    max-width: 380px;
  }
  .desc {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 380px;
  }
  select.inline {
    border: 1px solid transparent;
    background: transparent;
    padding: 2px 4px;
    font: inherit;
    color: inherit;
  }
  select.inline:hover,
  select.inline:focus {
    border-color: var(--border);
    background: var(--bg);
  }
  select.uncat {
    color: var(--warn);
    font-weight: 600;
  }
  .src {
    font-size: 11px;
    padding: 1px 5px;
    border-radius: 4px;
    margin-left: 4px;
    background: var(--soft);
    color: var(--muted);
  }
  .src-user {
    color: var(--accent);
  }
  .pager {
    display: flex;
    gap: 12px;
    align-items: center;
    justify-content: center;
    margin-top: 12px;
  }
  .notice {
    background: var(--soft);
    padding: 8px 12px;
    border-radius: 8px;
    font-size: 14px;
  }
</style>
