# Ledger

Your personal accountant, running on your own machine. Ledger reads the statements you download from
**Banca Transilvania, ING and Revolut** (CSV or PDF) and turns them into a **double-entry ledger**:

- every transaction gets a **category**. A local language model sorts the merchants it has not seen,
  and Ledger **learns from your corrections**: fixing one KAUFLAND payment fixes all of them, and
  future imports follow your choice;
- **subscriptions and regular bills** are found by themselves, with their price changes. So are
  **double charges, unusual amounts and new fees**;
- a **3-month forecast** of your balance, as a range with odds, that you can backtest against what
  really happened;
- **questions in plain Romanian or English** ("Cât am cheltuit pe mâncare în martie față de
  februarie?"). The model writes SQL, and Ledger checks that SQL and runs it read-only. The answer
  comes with the query, so you can see how it was computed.

Nothing leaves your computer. The language model is [Qwen2.5-7B-Instruct](https://huggingface.co/Qwen/Qwen2.5-7B-Instruct),
run by [llama.cpp](https://github.com/ggml-org/llama.cpp) on your own machine. Ledger uses no API keys
and no cloud services. The ledger itself is a single DuckDB file.

| Part | Language |
| --- | --- |
| Server, statement importers, ledger, insights, text-to-SQL checks | Go |
| Storage, views, the queries the model writes | SQL (DuckDB) |
| Classifier that learns from corrections, Monte Carlo forecast, evaluation harness, synthetic statements | Python (numpy) |
| Web UI | TypeScript, Svelte 5 |

## Running it

You need Go 1.27, Python 3.10+ with numpy, Node 22 and a C compiler for cgo. On Windows that is
MinGW-w64 gcc, from MSYS2 or WinLibs.

```bash
pip install -r ml/requirements.txt
bash scripts/setup-model.sh          # Qwen2.5-7B-Instruct Q4_K_M, 4.7 GB, into models/
bash scripts/setup-llama.sh vulkan   # llama-server into tools/llama.cpp (cpu | vulkan | cuda)
bash scripts/build.sh                # the web UI, embedded in server/bin/ledger
server/bin/ledger serve              # http://127.0.0.1:8080/
```

On Windows, run the scripts from Git Bash. The program is `server/bin/ledger.exe`.

Then drop your statements on the **Import** page, or use the command line:

```text
ledger import <files...>        read, check and store statements (re-importing is harmless)
ledger rates <file>             exchange rates: the BNR's XML (nbrfxrates.xml) or date,currency,ron CSV
ledger categorize [--relabel]   sort what is not sorted yet
ledger correct <key> <category> set a category by hand, for every transaction of that merchant
ledger transactions | recurring | alerts | forecast [--days 90] [--from YYYY-MM-DD]
ledger ask "Cât am dat pe benzină anul ăsta?"
ledger serve [--addr 127.0.0.1:8080]
common flags: --db data/ledger.duckdb   --no-llm   --json
```

Ledger starts llama-server itself when it first needs the model, on `127.0.0.1:8091` (or
`LEDGER_LLAMA_PORT`). It reuses one that is already running there. The server is tied to Ledger's
process: it stops when Ledger exits, even when Ledger is killed. On Windows that is a job object, on
Linux the parent-death signal. To use your own server, set `LEDGER_LLM_URL` (any server with the OpenAI
chat completions API and JSON-schema output). To use another model, set `LEDGER_MODEL`. Without a model
(or with `--no-llm`), everything works except asking questions. Categories then come only from the
rules, your corrections and the classifier.

> **Windows and DuckDB.** The Go DuckDB driver ships a static library that current MinGW
> toolchains cannot link (its C++ runtime does not match). On Windows, `scripts/go.sh` therefore links
> the official DuckDB 2.0 DLL instead (fetched and checksummed by `scripts/setup-duckdb.sh`), and
> `build.sh` copies `duckdb.dll` next to the program. Linux and macOS link statically and need none of
> this. Use `bash scripts/go.sh test ./...` in place of `go test ./...` on every system.

## The screens

- **Overview**: net worth across accounts (EUR converted at the BNR rate of each day), spending per
  month stacked by category with income on top, balance history, and top merchants.
- **Transactions**: search and filters. The category is a dropdown on each line: changing it corrects
  the whole merchant. A small tag says where each category came from (`rule`, `user`, `model`, `llm`).
- **Subscriptions & alerts**: what you pay on a schedule and what it costs a year. It shows price
  changes ("59,99 RON, was 49,99 RON before 5 Mar 2026") and the alerts.
- **Forecast**: the band of likely balances for 1–3 months, the lowest point, and the chance of going
  below a threshold. A backtest switch shows how the forecast would have done.
- **Ask**: questions, the answer as a number or a table, and the SQL behind it.
- **Import**: drag and drop, a per-file report (new lines, lines already known, warnings), and the
  categorization progress.

## How it works

### Reading statements

Each bank has its own importer (`server/internal/importer`):

| | BT | ING | Revolut |
| --- | --- | --- | --- |
| CSV | the header block, `dd.mm.yyyy`, debit and credit columns, running balance | details spread over several rows per transaction, initial and final balance | ISO timestamps, fees in their own column, several currencies, rows that never completed |
| PDF | text positions read from the PDF: columns found from the header, wrapped descriptions joined | | |

All three accept the separators, decimal formats (`1.234,56` and `1234.56`) and encodings that the
banks actually produce, including UTF-8 with a BOM and Windows-1250. Amounts are kept as integer
minor units (bani, cents) end to end, so there is no floating-point money anywhere.

Every statement is **checked against itself**. When the export carries running balances or an
opening and closing balance, each line must take the balance from one value to the next. A misread
amount, a skipped row or a lost minus sign fails the import with the line number. It is not stored.

Each line gets a **stable key** (`account|currency|date|amount|occurrence`). Overlapping exports and
the same month as both PDF and CSV are therefore imported once. A declined or reverted Revolut payment
is skipped.

### Double entry

The ledger (`server/internal/store/build.go`) is rebuilt from the bank lines whenever something
changes, so it is always consistent with them.

- Each line becomes a transaction with balanced postings: `Assets:BT:RON` against `Expenses:Groceries`,
  and `Income:Salary` against `Assets:ING:RON`.
- **Transfers between your own accounts** are matched across banks. Ledger pairs the outgoing and
  incoming sides with the same amount within four days. A pair must have enough evidence:
  - the other account's IBAN in the description;
  - the other bank's code (`BTRL`, `INGB`, `REVO`) or name;
  - a Revolut top-up;
  - your own name as the counterparty.
- Unmatched sides wait in `Transit:Transfers in transit`.
- **Currency exchanges** go through `Equity:Currency trading`.
- The first balance of each account comes from `Equity:Opening balances`.

On the test statements, all 122 transfer lines and all 26 exchange lines were paired exactly as in the
truth.

The model never sees the tables. It sees four **views**:

- `transactions`: every movement, signed, with its kind;
- `expenses`: spending as positive amounts, refunds negative;
- `income`;
- `balances`.

Every row has `amount_ron`, converted with an `ASOF` join on the exchange rate of its day.

### Categories, and learning from you

The steps run from the most to the least certain, and a step never overrides a more trusted one:

1. **Rules**: what the statement says outright. That covers a bank fee, an ATM withdrawal, interest,
   a salary, and a top-up between your accounts.
2. **Your corrections**: a category you set applies to the merchant. A merchant is a key extracted
   from the description (`Plata la POS non-BT cu card VISA;POS 27/09/2026 TID:37183852 MEGA IMAGE…`
   becomes `MEGA IMAGE`), so it covers every past and future payment there.
3. **The classifier** (`ml/ledger_ml/classify.py`): a softmax regression in numpy over hashed
   character n-grams of the merchant, the description words, the direction and the size of the
   amount. It is retrained on your corrections at each run. Trained on a handful of corrections, it
   would be confidently wrong about every merchant it has not seen. So its answer only counts when
   the merchant resembles one you corrected (cosine similarity of n-grams ≥ 0.6, so `MEGA IMAGE
   EXPRES` matches `MEGA IMAGE`). Otherwise it abstains.
4. **The language model**, for the merchants left: batches of 8 merchants per call (not one call per
   transaction). The prompt has the category descriptions, your corrections as examples, and one sample
   description per merchant. The answer is **constrained by a JSON schema**: the merchant field is an
   enum of the batch's names, and the category is an enum of the real categories. The model therefore
   cannot invent a category, misspell a merchant or shift answers onto the wrong line.

### Subscriptions, alerts and the forecast

**Recurring payments** (`server/internal/insights`). Payments to the same merchant are recurring when
their intervals are steadily weekly, monthly or yearly and their amounts are stable. Seasonal bills
whose amount varies (gas in winter) also count, when they come every month. Ledger shows the next
expected date, what a year costs, and when and by how much a price changed.

**Alerts**:
- the same merchant, account and amount twice on one day (from 30 RON up);
- an amount far outside a merchant's usual range: robust z-score on a log scale with median and MAD,
  at least 15 past payments, and more than 5× the usual. Utilities are left out, since a winter gas
  bill is not an anomaly;
- a kind of fee you were not charged before.

**Forecast** (`ml/ledger_ml/forecast.py`). A Monte Carlo simulation of 2000 possible futures:
- the scheduled flows land on their dates. A flow whose amount varied in the past varies the same way;
- everyday spending is resampled from your own past days, the same weekday, over the last 26 weeks.

The result is the median and the 10–90% band per day, the lowest point, and the probability of the
balance going below a threshold.

### Questions → SQL, and why it is safe

The question goes to the model with the views' columns, the categories, a glossary of the words people
use for them ("mâncare" means three categories, "benzină" means Fuel), today's date and six examples.
The answer must be JSON `{"sql": "..."}`.

Ledger then **checks the query before running it** (`server/internal/ask`). DuckDB parses it
(`json_serialize_sql`), and Ledger walks the syntax tree:

- exactly one statement, and it must be a `SELECT`;
- it may read only the four views, or CTEs it defines itself;
- it may not use table functions (`read_csv`, `duckdb_settings()`) or functions that reach outside
  the views (`read_text`, `getenv`, `current_setting`).

The query then runs inside a transaction that is always rolled back, with a 10-second timeout and at
most 500 rows. The whole database has `enable_external_access=false` and `lock_configuration=true`: no
file, extension or network access, and the query cannot turn that back on.

If DuckDB rejects the query, the error goes back to the model for **one** retry. A failed answer
shows the rejected queries and why.

## Results

All of this is measured on **synthetic statements whose truth is known** (`testdata/synthetic`,
written by `ml/ledger_ml/synth.py`). They cover 18 months of one person's life in Bucharest, 1071
transactions over three banks and two currencies:

- salary, rent, bills and subscriptions;
- savings transfers and Revolut top-ups with EUR exchanges;
- a trip abroad;
- two overlapping ING exports, and the last BT month also as a PDF.

They also contain planted anomalies:
- a double eMAG charge;
- a 1980 RON Kaufland payment;
- a new 25 RON fee;
- Netflix going from 49,99 to 59,99.

The real banks' exports are reproduced in their formats; the people and companies are invented.

The evaluations run through the real `ledger` binary and the real local model (`python -m
ledger_ml.evaluate categories|questions`). They were run on an RTX 2060 with 6 GB, where llama.cpp fits
most of the 7B model on the GPU.

**Import**:
- 1071 lines from 6 files;
- 3 lines of overlap between the ING exports and all 30 lines of the PDF recognized as already known;
- 1 declined Revolut payment skipped;
- every running balance verified.

**Categories**: accuracy over all 1071 lines against the true categories. Each step corrects the
merchants with the most wrong lines, as a user would.

| Corrections | Accuracy |
| --- | --- |
| 0 (rules and the model only) | 85.9% |
| 3 | 94.4% |
| 6 | 99.8% |
| 8 | 99.6% |

With no corrections, the model's typical mistakes are reasonable guesses: Bolt rides taken for food
delivery (both say `BOLT.EU`), a restaurant whose name sounds like a shop. Each correction fixes a
whole merchant, and the classifier carries it to the merchant's variants. The first import with
categorization takes about 2 minutes: the model is asked about 50 merchants, not 1000 lines.

**Questions**: 25 questions in Romanian and English, each graded against the answer computed
independently from the truth.

- **24 of 25 right.**
- Median latency 3.5 s, slowest 9.7 s.
- One answer needed the retry.

The questions cover sums by category, merchant or period, comparisons, averages per month, top-N,
EUR amounts, counts, balances, net savings, and the subscriptions with what each cost. The one miss,
"Am cheltuit mai mult în iulie sau în august 2026?", came back broken down by category instead of a
total per month. The SQL shown with the answer makes such a slip easy to see.

**Forecast backtest**: forecast 90 days ahead as if the data ended on 30 June 2026, then compared with
what really happened.

- The real balance stayed inside the 10–90% band on **96% of the days**.
- At the end, the median said 60 861 RON and the real balance was 62 182 RON (the band was 57 898 to
  63 705).
- The average daily error was 882 RON.

**Insights**: the tests check every planted anomaly and recurring payment against the truth, and
require that there are no other alerts.

## Testing

```bash
bash scripts/go.sh test ./...                              # Go: importers, ledger, insights, forecast, SQL checks, API
cd ml && python -m unittest discover -s tests -t .         # Python: classifier, forecast, synthetic data
cd web && npm run check && npm run build                   # the UI
cd ml && python -m ledger_ml.evaluate categories           # with the model: categories, learning curve
cd ml && python -m ledger_ml.evaluate questions            # with the model: the 25 questions
```

- **Importers**: each bank and format against the truth, overlapping exports, a misread row caught by
  the balance check, Windows-1250 and semicolons, wrapped PDF descriptions.
- **Store**: deduplication, every transaction balanced, transfers and exchanges paired, the views
  answering with the true categories, external access refused.
- **Text-to-SQL checks**: deletes, inserts, `ATTACH`, multiple statements, the internal tables, table
  functions, `read_text` and `getenv` are all refused, and valid queries pass. A retry after an error
  is tested with a fake model.
- **API**: import through multipart upload, re-import, search, correction of a whole merchant,
  questions answered and refused, cross-origin writes refused.
- **Python**: the classifier learns and abstains on unknown merchants, the forecast is exact for a
  steady life and widens with uncertain spending, and the committed test statements are byte for byte
  what the generator writes.

CI runs the Go and Python tests on Ubuntu and Windows and builds the UI. The evaluations with the model
run locally only.

## Layout

```text
server/                 Go module
  cmd/ledger/           the command line and `serve`
  internal/importer/    BT, ING, Revolut; CSV and PDF; balance checks
  internal/merchant/    merchant keys from bank descriptions
  internal/store/       DuckDB schema, import, double-entry rebuild, views, labels, rates
  internal/categorize/  rules → corrections → classifier → language model
  internal/insights/    recurring payments, alerts
  internal/forecast/    daily balances and the call to the Python forecast
  internal/ask/         question → SQL, the checks, the read-only run
  internal/llm/         llama-server client and process
  internal/ml/          the bridge to Python (JSON over stdin/stdout)
  internal/api/         HTTP API; the built UI is embedded from dist/
ml/ledger_ml/           classify.py, forecast.py, evaluate.py, synth.py
ml/eval/                the latest evaluation results (JSON)
web/                    Svelte 5 + TypeScript + Vite
testdata/synthetic/     the generated statements and their truth
scripts/                setup-duckdb.sh, setup-llama.sh, setup-model.sh, go.sh, build.sh
```

Your own data lives in `data/` (the DuckDB file, the classifier's weights, the llama-server log).
That folder is ignored by git.

## License

MIT
