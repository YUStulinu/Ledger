<script lang="ts">
  import { api, ApiError, type Answer, type Status } from "../lib/api";
  import { money } from "../lib/format";

  let { status }: { status: Status | null } = $props();

  let question = $state("");
  let busy = $state(false);
  let answer = $state<Answer | null>(null);
  let error = $state("");
  let failed = $state<Answer | null>(null);
  let history = $state<string[]>([]);

  const examples = [
    "Cât am cheltuit pe mâncare luna trecută?",
    "How much did I spend per category in 2026?",
    "Care sunt cei mai scumpi 5 comercianți din ultimele 3 luni?",
    "Cât m-a costat Netflix în total?",
    "What is my average monthly spending on transport?",
    "Am cheltuit mai mult în iulie sau în august 2026?",
  ];

  async function ask(q = question) {
    q = q.trim();
    if (!q || busy) return;
    question = q;
    busy = true;
    error = "";
    failed = null;
    try {
      answer = await api.ask(q);
      history = [q, ...history.filter((h) => h !== q)].slice(0, 8);
    } catch (e: any) {
      answer = null;
      error = e.message;
      if (e instanceof ApiError) failed = e.body?.answer ?? null;
    } finally {
      busy = false;
    }
  }

  // A column that holds money is shown as money; everything else as it is.
  const moneyCol = (name: string) => /ron|amount|spent|total|cost|sum|income|net|eur|avg|average|balance/i.test(name);
  function cell(v: unknown, col: string): string {
    if (v === null || v === undefined) return "—";
    if (typeof v === "number") return moneyCol(col) ? money(v, /eur/i.test(col) ? "EUR" : "RON") : String(v);
    return String(v).replace(/T00:00:00Z$/, "");
  }
</script>

<section class="card">
  <form
    class="ask"
    onsubmit={(e) => {
      e.preventDefault();
      ask();
    }}
  >
    <input type="text" placeholder="Ask about your money, in Romanian or English" bind:value={question} disabled={busy} />
    <button type="submit" disabled={busy || !question.trim() || !status?.llm}>{busy ? "Thinking…" : "Ask"}</button>
  </form>
  {#if status && !status.llm}
    <p class="warn small">The local language model is not installed (models/{status.model}): see the README.</p>
  {/if}
  <div class="examples">
    {#each history.length ? history : examples as q}
      <button class="chip" onclick={() => ask(q)} disabled={busy}>{q}</button>
    {/each}
  </div>
  <p class="muted small">
    The local model writes one SQL query; Ledger checks that it only reads the finance views, runs it read-only with a time limit,
    and shows you the query with the answer.
  </p>
</section>

{#if error}
  <section class="card">
    <p class="error">{error}</p>
    {#if failed?.rejected?.length}
      <details>
        <summary>Queries that were rejected</summary>
        {#each failed.rejected as r}
          <pre>{r.sql}</pre>
          <p class="small error">{r.error}</p>
        {/each}
      </details>
    {/if}
  </section>
{/if}

{#if answer}
  <section class="card">
    <header>
      <h2>{answer.question}</h2>
      <span class="muted small">{answer.seconds.toFixed(1)} s</span>
    </header>
    {#if answer.rows.length === 1 && answer.columns.length === 1}
      <p class="big">{cell(answer.rows[0][0], answer.columns[0])}</p>
    {:else if answer.rows.length === 0}
      <p class="muted">No rows: nothing matches.</p>
    {:else}
      <div class="scroll">
        <table>
          <thead><tr>{#each answer.columns as c}<th>{c}</th>{/each}</tr></thead>
          <tbody>
            {#each answer.rows as row}
              <tr>
                {#each row as v, i}
                  <td class:num={typeof v === "number"}>{cell(v, answer.columns[i])}</td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    <details open={false}>
      <summary>SQL{answer.rejected?.length ? ` (second try: the first was rejected)` : ""}</summary>
      <pre>{answer.sql}</pre>
      {#each answer.rejected ?? [] as r}
        <p class="small muted">Rejected first: {r.error}</p>
        <pre class="rejected">{r.sql}</pre>
      {/each}
    </details>
  </section>
{/if}

<style>
  section + section {
    margin-top: 16px;
  }
  .ask {
    display: flex;
    gap: 8px;
  }
  .ask input {
    flex: 1;
    font-size: 16px;
    padding: 10px 12px;
  }
  .examples {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 12px;
  }
  .chip {
    font-size: 13px;
    padding: 4px 10px;
    border-radius: 999px;
    background: var(--soft);
    border: 1px solid var(--border);
    color: var(--text);
  }
  .big {
    font-size: 32px;
    font-weight: 650;
    margin: 8px 0;
    font-variant-numeric: tabular-nums;
  }
  pre {
    background: var(--soft);
    padding: 10px 12px;
    border-radius: 8px;
    overflow-x: auto;
    white-space: pre-wrap;
    font-size: 13px;
  }
  .rejected {
    opacity: 0.7;
  }
  .scroll {
    overflow-x: auto;
  }
  details {
    margin-top: 12px;
  }
  summary {
    cursor: pointer;
    color: var(--muted);
    font-size: 14px;
  }
</style>
