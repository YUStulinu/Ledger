<script lang="ts">
  import { api, type Category, type Status } from "./lib/api";
  import Ask from "./pages/Ask.svelte";
  import Forecast from "./pages/Forecast.svelte";
  import Import from "./pages/Import.svelte";
  import Insights from "./pages/Insights.svelte";
  import Overview from "./pages/Overview.svelte";
  import Transactions from "./pages/Transactions.svelte";

  const pages = [
    { path: "overview", title: "Overview" },
    { path: "transactions", title: "Transactions" },
    { path: "insights", title: "Subscriptions & alerts" },
    { path: "forecast", title: "Forecast" },
    { path: "ask", title: "Ask" },
    { path: "import", title: "Import" },
  ];

  // Routes live in the hash: #/transactions?q=KAUFLAND
  function parse() {
    const [path, query] = location.hash.replace(/^#\/?/, "").split("?");
    return { path: pages.some((p) => p.path === path) ? path : "overview", params: new URLSearchParams(query ?? "") };
  }
  let route = $state(parse());
  $effect(() => {
    const on = () => (route = parse());
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  });

  let status = $state<Status | null>(null);
  let categories = $state<Category[]>([]);
  let error = $state("");
  // Bumped when data changes (an import, a correction) so pages reload.
  let version = $state(0);

  function refresh() {
    api.status().then((s) => ((status = s), (error = "")), (e) => (error = `The server is not answering: ${e.message}`));
  }
  $effect(() => {
    refresh();
    api.categories().then((c) => (categories = c));
  });
  function changed() {
    refresh();
    version++;
  }

  const title = $derived(pages.find((p) => p.path === route.path)?.title ?? "");
  $effect(() => {
    document.title = `${title} · Ledger`;
  });
</script>

<div class="app">
  <nav>
    <a class="brand" href="#/overview">Ledger</a>
    <div class="links">
      {#each pages as p}
        <a href="#/{p.path}" class:active={route.path === p.path}>
          {p.title}
          {#if p.path === "transactions" && status?.uncategorized}<span class="badge">{status.uncategorized}</span>{/if}
        </a>
      {/each}
    </div>
    {#if status}
      <div class="meta muted small">
        {status.lines} lines · data to {status.data_end}
        <span class="llm" class:off={!status.llm} title={status.model}>{status.llm ? "local LLM ready" : "no LLM"}</span>
      </div>
    {/if}
  </nav>

  <main>
    <h1>{title}</h1>
    {#if error}<p class="error">{error}</p>{/if}
    {#key version + route.path}
      {#if route.path === "overview"}
        <Overview {status} />
      {:else if route.path === "transactions"}
        <Transactions params={route.params} {categories} onchange={refresh} />
      {:else if route.path === "insights"}
        <Insights />
      {:else if route.path === "forecast"}
        <Forecast {status} />
      {:else if route.path === "ask"}
        <Ask {status} />
      {:else if route.path === "import"}
        <Import {status} onchange={changed} />
      {/if}
    {/key}
  </main>
</div>

<style>
  .app {
    display: grid;
    grid-template-columns: 220px minmax(0, 1fr);
    min-height: 100vh;
  }
  nav {
    border-right: 1px solid var(--border);
    padding: 20px 12px;
    display: flex;
    flex-direction: column;
    gap: 16px;
    position: sticky;
    top: 0;
    height: 100vh;
    background: var(--panel);
  }
  .brand {
    font-size: 20px;
    font-weight: 700;
    color: var(--text);
    text-decoration: none;
    padding: 0 10px;
    letter-spacing: -0.02em;
  }
  .links {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .links a {
    padding: 7px 10px;
    border-radius: 8px;
    color: var(--text);
    text-decoration: none;
    font-size: 14.5px;
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  .links a:hover {
    background: var(--soft);
  }
  .links a.active {
    background: var(--soft);
    color: var(--accent);
    font-weight: 600;
  }
  .badge {
    font-size: 11px;
    background: var(--warn);
    color: #fff;
    border-radius: 999px;
    padding: 1px 7px;
  }
  .meta {
    margin-top: auto;
    padding: 0 10px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .llm::before {
    content: "●";
    color: var(--good);
    margin-right: 5px;
  }
  .llm.off::before {
    color: var(--muted);
  }
  main {
    padding: 24px 28px 48px;
    max-width: 1280px;
    width: 100%;
  }
  h1 {
    font-size: 24px;
    margin: 0 0 16px;
    letter-spacing: -0.02em;
  }
  @media (max-width: 760px) {
    .app {
      grid-template-columns: minmax(0, 1fr);
    }
    nav {
      position: static;
      height: auto;
      border-right: none;
      border-bottom: 1px solid var(--border);
      padding: 12px 16px;
    }
    .links {
      flex-direction: row;
      flex-wrap: wrap;
    }
    .meta {
      display: none;
    }
    main {
      padding: 16px;
    }
  }
</style>
