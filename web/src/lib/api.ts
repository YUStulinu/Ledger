// The server's JSON API (server/internal/api).

export interface Status {
  lines: number;
  uncategorized: number;
  llm: boolean;
  data_end: string;
  model: string;
}

export interface Category {
  Name: string;
  Kind: "expense" | "income" | "transfer" | "other";
  Hint: string;
  Assignable: boolean;
}

export interface Overview {
  balances: { bank: string; account: string; currency: string; balance: number }[] | null;
  monthly: { month: string; category: string; amount: number }[] | null;
  income: { month: string; amount: number }[] | null;
  top_merchants: { merchant: string; category: string; amount: number; count: number }[] | null;
  balance_history: { date: string; ron: number }[] | null;
}

export interface Tx {
  id: number;
  date: string;
  bank: string;
  currency: string;
  amount: number;
  kind: string;
  category: string;
  merchant: string;
  description: string;
  key: string;
  source: string;
}

export interface Recurring {
  merchant: string;
  category: string;
  currency: string;
  cadence: "weekly" | "monthly" | "yearly";
  count: number;
  first: string;
  last: string;
  next: string;
  amount: number;
  typical: number;
  typical_ron: number;
  yearly_ron: number;
  active: boolean;
  income: boolean;
  previous_amount?: number;
  changed_on?: string;
  new: boolean;
}

export interface InsightTx {
  id: number;
  date: string;
  account: string;
  currency: string;
  amount: number;
  amount_ron: number;
  category: string;
  merchant: string;
  description: string;
}

export interface Alert {
  kind: "duplicate" | "unusual_amount" | "new_fee";
  transaction: InsightTx;
  message: string;
  other?: InsightTx;
}

export interface Forecast {
  start_balance: number;
  days: string[];
  p10: number[];
  p50: number[];
  p90: number[];
  end: { p10: number; p50: number; p90: number };
  lowest: { date: string; p50: number; p10: number };
  threshold: number;
  chance_below_threshold: number;
  everyday_spending_per_month: number;
  scheduled: number;
  history: { date: string; ron: number }[] | null;
  actual?: number[];
  coverage_10_90?: number;
  mean_abs_error?: number;
  end_error?: number;
}

export interface Answer {
  question: string;
  sql: string;
  columns: string[];
  rows: unknown[][];
  rejected?: { sql: string; error: string }[];
  seconds: number;
}

export interface ImportResult {
  file: string;
  bank: string;
  format: string;
  account: string;
  rows: number;
  new: number;
  duplicates: number;
  warnings?: string[];
  already_imported?: boolean;
}

export interface Job {
  kind?: string;
  running: boolean;
  done?: number;
  total?: number;
  error?: string;
  result?: { rules: number; corrections: number; model: number; llm: number; left: number; merchants_asked: number };
  started?: string;
  finished?: string;
}

export class ApiError extends Error {
  constructor(message: string, readonly body: any) {
    super(message);
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  const text = await res.text();
  let body: any = null;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    throw new ApiError(text || res.statusText, null);
  }
  if (!res.ok) throw new ApiError(body?.error ?? res.statusText, body);
  return body as T;
}

const post = (body: unknown): RequestInit => ({
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const api = {
  status: () => call<Status>("/api/status"),
  categories: () => call<Category[]>("/api/categories"),
  overview: () => call<Overview>("/api/overview"),
  transactions: (params: Record<string, string | number | undefined>) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== "") q.set(k, String(v));
    return call<{ total: number; items: Tx[] }>("/api/transactions?" + q);
  },
  correct: (key: string, category: string) => call<{ updated: number }>("/api/transactions/category", post({ key, category })),
  import: (files: File[]) => {
    const form = new FormData();
    for (const f of files) form.append("files", f, f.name);
    return call<{ imported: ImportResult[]; job?: Job; note?: string }>("/api/import", { method: "POST", body: form });
  },
  categorize: () => call<Job>("/api/categorize", { method: "POST" }),
  job: () => call<Job>("/api/job"),
  recurring: () => call<Recurring[] | null>("/api/recurring"),
  alerts: () => call<Alert[] | null>("/api/alerts"),
  forecast: (days = 90, threshold = 0, from = "") => {
    const q = new URLSearchParams({ days: String(days), threshold: String(threshold) });
    if (from) q.set("from", from);
    return call<Forecast>("/api/forecast?" + q);
  },
  ask: (question: string) => call<Answer>("/api/ask", post({ question })),
};
