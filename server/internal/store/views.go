package store

// The views that questions are answered from. They are the only names the text-to-SQL prompt
// describes and the only tables its queries may read, so they are kept few and self-explanatory:
// every amount comes both in its own currency and converted to RON at that day's rate (the last
// rate published on or before the date, which is what ASOF JOIN picks).
const views = `
CREATE OR REPLACE VIEW transactions AS
WITH lines AS (
	SELECT t.id, t.date, t.kind, t.category, t.merchant, t.description,
	       a.bank, p.account, p.currency, p.amount / 100.0 AS amount
	FROM ledger_postings p
	JOIN ledger_transactions t ON t.id = p.tx_id
	JOIN ledger_accounts a ON a.name = p.account
	WHERE a.type = 'asset' AND t.kind <> 'opening'
)
SELECT l.id, l.date, year(l.date) AS year, month(l.date) AS month,
       CAST(date_trunc('month', l.date) AS DATE) AS month_start,
       l.bank, l.account, l.currency,
       CAST(l.amount AS DECIMAL(18, 2)) AS amount,
       CAST(round(l.amount * coalesce(r.ron, 1), 2) AS DECIMAL(18, 2)) AS amount_ron,
       l.kind, l.category, l.merchant, l.description
FROM lines l
ASOF LEFT JOIN rates r ON r.currency = l.currency AND l.date >= r.date;

CREATE OR REPLACE VIEW expenses AS
SELECT id, date, year, month, month_start, bank, account, currency,
       -amount AS amount, -amount_ron AS amount_ron, category, merchant, description
FROM transactions
WHERE kind = 'expense';

CREATE OR REPLACE VIEW income AS
SELECT id, date, year, month, month_start, bank, account, currency, amount, amount_ron, category, merchant, description
FROM transactions
WHERE kind = 'income';

CREATE OR REPLACE VIEW balances AS
SELECT a.bank, p.account, p.currency, CAST(sum(p.amount) / 100.0 AS DECIMAL(18, 2)) AS balance
FROM ledger_postings p
JOIN ledger_accounts a ON a.name = p.account
WHERE a.type = 'asset'
GROUP BY ALL;
`

// The views' columns, for the text-to-SQL prompt and the query checker.
var ViewColumns = map[string][]string{
	"transactions": {"id", "date", "year", "month", "month_start", "bank", "account", "currency", "amount", "amount_ron", "kind", "category", "merchant", "description"},
	"expenses":     {"id", "date", "year", "month", "month_start", "bank", "account", "currency", "amount", "amount_ron", "category", "merchant", "description"},
	"income":       {"id", "date", "year", "month", "month_start", "bank", "account", "currency", "amount", "amount_ron", "category", "merchant", "description"},
	"balances":     {"bank", "account", "currency", "balance"},
}
