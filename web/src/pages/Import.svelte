<script lang="ts">
  import { api, type ImportResult, type Job, type Status } from "../lib/api";

  let { status, onchange }: { status: Status | null; onchange: () => void } = $props();

  let dragging = $state(false);
  let busy = $state(false);
  let error = $state("");
  let results = $state<ImportResult[]>([]);
  let job = $state<Job | null>(null);
  let input: HTMLInputElement;

  async function upload(list: FileList | File[] | null) {
    const files = [...(list ?? [])];
    if (!files.length) return;
    busy = true;
    error = "";
    try {
      const res = await api.import(files);
      results = res.imported;
      if (res.note) error = res.note;
      onchange();
      poll();
    } catch (e: any) {
      error = e.message;
    } finally {
      busy = false;
      if (input) input.value = "";
    }
  }

  async function categorize() {
    error = "";
    try {
      job = await api.categorize();
      poll();
    } catch (e: any) {
      error = e.message;
    }
  }

  let timer: ReturnType<typeof setTimeout> | undefined;
  async function poll() {
    clearTimeout(timer);
    try {
      job = await api.job();
    } catch {
      return;
    }
    if (job.running) timer = setTimeout(poll, 1000);
    else onchange();
  }

  $effect(() => {
    poll();
    return () => clearTimeout(timer);
  });

  function drop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    upload(e.dataTransfer?.files ?? null);
  }
</script>

<section class="card">
  <header><h2>Import statements</h2></header>
  <div
    class="drop"
    class:dragging
    role="button"
    tabindex="0"
    ondragover={(e) => {
      e.preventDefault();
      dragging = true;
    }}
    ondragleave={() => (dragging = false)}
    ondrop={drop}
    onclick={() => input.click()}
    onkeydown={(e) => (e.key === "Enter" || e.key === " ") && input.click()}
  >
    <strong>{busy ? "Reading…" : "Drop statements here, or click to choose files"}</strong>
    <span class="muted">CSV and PDF exports from Banca Transilvania, ING and Revolut. Overlapping exports are fine: lines already imported are skipped.</span>
  </div>
  <input bind:this={input} type="file" multiple accept=".csv,.pdf,text/csv,application/pdf" hidden onchange={(e) => upload(e.currentTarget.files)} />
  {#if error}<p class="error">{error}</p>{/if}

  {#if results.length}
    <table>
      <thead><tr><th>File</th><th>Bank</th><th>Account</th><th class="num">Lines</th><th class="num">New</th><th class="num">Already there</th></tr></thead>
      <tbody>
        {#each results as r}
          <tr>
            <td>{r.file}<span class="muted small"> · {r.format}</span></td>
            <td>{r.bank}</td>
            <td class="mono small">{r.account}</td>
            <td class="num">{r.rows}</td>
            <td class="num">{r.new}</td>
            <td class="num">{r.duplicates}</td>
          </tr>
          {#each r.warnings ?? [] as w}
            <tr><td colspan="6" class="warn small">⚠ {w}</td></tr>
          {/each}
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<section class="card">
  <header>
    <h2>Categories</h2>
    <button onclick={categorize} disabled={job?.running}>Categorize again</button>
  </header>
  <p class="muted">
    Rules first, then your corrections, then a classifier trained on them, then the local language model
    {#if status}({status.llm ? status.model : "not installed: see the README"}){/if} for the merchants left.
  </p>
  {#if job?.running}
    <div class="progress">
      <div class="bar" style:width="{job.total ? (100 * (job.done ?? 0)) / job.total : 5}%"></div>
    </div>
    <p class="muted small">
      {job.total ? `Asking the language model: ${job.done} of ${job.total} merchants` : "Applying rules and corrections…"}
    </p>
  {:else if job?.error}
    <p class="error">{job.error}</p>
  {:else if job?.result}
    {@const r = job.result}
    <p>
      Last run: <strong>{r.rules}</strong> by rules, <strong>{r.corrections}</strong> from your corrections,
      <strong>{r.model}</strong> by the classifier, <strong>{r.llm}</strong> by the language model ({r.merchants_asked} merchants);
      <strong>{r.left}</strong> left uncategorized.
    </p>
  {/if}
</section>

<style>
  .drop {
    border: 2px dashed var(--border);
    border-radius: 12px;
    padding: 40px 16px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    align-items: center;
    text-align: center;
    cursor: pointer;
    transition: border-color 0.15s, background 0.15s;
  }
  .drop:hover,
  .drop.dragging {
    border-color: var(--accent);
    background: var(--soft);
  }
  table {
    margin-top: 16px;
  }
  .progress {
    height: 8px;
    background: var(--soft);
    border-radius: 4px;
    overflow: hidden;
  }
  .bar {
    height: 100%;
    background: var(--accent);
    transition: width 0.4s;
  }
  section + section {
    margin-top: 16px;
  }
</style>
