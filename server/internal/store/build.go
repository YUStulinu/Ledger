package store

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/category"
)

type line struct {
	Key, Bank, Account, Currency    string
	Seq                             int64
	Date                            time.Time
	Time                            string
	Amount                          int64
	Description, Reference, Type    string
	Merchant, MerchantKey, Category string
}

type posting struct {
	account, currency, lineKey string
	amount                     int64
}

type ledgerTx struct {
	id                                    int64
	date                                  time.Time
	kind, category, merchant, description string
	postings                              []posting
}

// BuildStats summarizes a rebuild.
type BuildStats struct {
	Lines         int `json:"lines"`
	Transactions  int `json:"transactions"`
	Transfers     int `json:"transfers"`
	Exchanges     int `json:"exchanges"`
	Uncategorized int `json:"uncategorized"`
	// Lines marked as transfers whose other side is not in any imported statement.
	UnmatchedTransfers int `json:"unmatched_transfers"`
}

// Where an unmatched transfer's other side goes until the statement of the other account is imported.
const transitAccount = "Transit:Transfers in transit"

var ibanLike = regexp.MustCompile(`^[A-Z]{2}\d{2}[A-Z0-9]{10,30}$`)

// Rebuild derives the ledger from the bank lines, their categories, the openings and the rates.
func (s *Store) Rebuild(ctx context.Context) (*BuildStats, error) {
	lines, err := s.loadLines(ctx)
	if err != nil {
		return nil, err
	}
	owners, err := s.owners(ctx)
	if err != nil {
		return nil, err
	}
	names := assetNames(lines)
	stats := &BuildStats{Lines: len(lines)}
	var txs []ledgerTx
	used := make([]bool, len(lines))

	// Opening balances.
	openings, err := s.DB.QueryContext(ctx, "SELECT account, currency, date, amount FROM openings ORDER BY date")
	if err != nil {
		return nil, err
	}
	for openings.Next() {
		var account, cur string
		var date time.Time
		var amount int64
		if err := openings.Scan(&account, &cur, &date, &amount); err != nil {
			openings.Close()
			return nil, err
		}
		asset := names.of(account, cur)
		txs = append(txs, ledgerTx{date: date, kind: "opening", category: "Opening balance", merchant: "Opening balance",
			postings: []posting{{asset, cur, "", amount}, {"Equity:Opening balances", cur, "", -amount}}})
	}
	openings.Close()

	// Currency exchanges: the two legs share a moment (Revolut lists them as EXCHANGE rows).
	byMoment := map[string][]int{}
	for i, l := range lines {
		if strings.EqualFold(l.Type, "EXCHANGE") {
			byMoment[l.Account+"|"+l.Date.Format("2006-01-02")+"|"+l.Time] = append(byMoment[l.Account+"|"+l.Date.Format("2006-01-02")+"|"+l.Time], i)
		}
	}
	for _, idx := range byMoment {
		var out, in = -1, -1
		for _, i := range idx {
			if lines[i].Amount < 0 && out < 0 {
				out = i
			} else if lines[i].Amount > 0 && in < 0 {
				in = i
			}
		}
		if out < 0 || in < 0 || lines[out].Currency == lines[in].Currency {
			continue
		}
		o, n := lines[out], lines[in]
		used[out], used[in] = true, true
		stats.Exchanges++
		txs = append(txs, ledgerTx{date: o.Date, kind: "exchange", category: category.Exchange,
			merchant: fmt.Sprintf("%s → %s", o.Currency, n.Currency), description: o.Description + " / " + n.Description,
			postings: []posting{
				{names.of(o.Account, o.Currency), o.Currency, o.Key, o.Amount},
				{"Equity:Currency trading", o.Currency, "", -o.Amount},
				{names.of(n.Account, n.Currency), n.Currency, n.Key, n.Amount},
				{"Equity:Currency trading", n.Currency, "", -n.Amount},
			}})
	}

	// Transfers between your own accounts: an outflow and an inflow of the same amount and currency,
	// on two different accounts, within four days, with something that says so (the other account's
	// IBAN, the bank's name, a top-up, your own name).
	type candidate struct {
		out, in, score int
		days           float64
	}
	var cands []candidate
	inflows := map[string][]int{} // currency|amount -> lines
	for j, n := range lines {
		if !used[j] && n.Amount > 0 {
			k := fmt.Sprintf("%s|%d", n.Currency, n.Amount)
			inflows[k] = append(inflows[k], j)
		}
	}
	for i, o := range lines {
		if used[i] || o.Amount >= 0 {
			continue
		}
		for _, j := range inflows[fmt.Sprintf("%s|%d", o.Currency, -o.Amount)] {
			n := lines[j]
			if n.Account == o.Account {
				continue
			}
			days := n.Date.Sub(o.Date).Hours() / 24
			if days < -1 || days > 4 {
				continue
			}
			if sc := transferScore(o, n, owners); sc >= 2 {
				cands = append(cands, candidate{i, j, sc, abs(days)})
			}
		}
	}
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].score != cands[b].score {
			return cands[a].score > cands[b].score
		}
		if cands[a].days != cands[b].days {
			return cands[a].days < cands[b].days
		}
		return lines[cands[a].out].Seq < lines[cands[b].out].Seq
	})
	for _, c := range cands {
		if used[c.out] || used[c.in] {
			continue
		}
		o, n := lines[c.out], lines[c.in]
		used[c.out], used[c.in] = true, true
		stats.Transfers++
		txs = append(txs, ledgerTx{date: o.Date, kind: "transfer", category: category.Transfer,
			merchant: fmt.Sprintf("%s → %s", o.Bank, n.Bank), description: o.Description,
			postings: []posting{{names.of(o.Account, o.Currency), o.Currency, o.Key, o.Amount}, {names.of(n.Account, n.Currency), n.Currency, n.Key, n.Amount}}})
	}

	// Everything else: one posting on the bank account, the other on the category's account.
	for i, l := range lines {
		if used[i] {
			continue
		}
		cat := l.Category
		if cat == "" {
			cat = category.Uncategorized
			stats.Uncategorized++
		}
		c, _ := category.Lookup(cat)
		kind, other := "expense", "Expenses:"+cat
		switch {
		case cat == category.Transfer || cat == category.Exchange:
			kind, other = "transfer", transitAccount
			stats.UnmatchedTransfers++
		case c.Kind == category.Income:
			kind, other = "income", "Income:"+cat
		}
		txs = append(txs, ledgerTx{date: l.Date, kind: kind, category: cat, merchant: displayMerchant(l), description: l.Description,
			postings: []posting{{names.of(l.Account, l.Currency), l.Currency, l.Key, l.Amount}, {other, l.Currency, "", -l.Amount}}})
	}

	sort.SliceStable(txs, func(a, b int) bool { return txs[a].date.Before(txs[b].date) })
	for i := range txs {
		txs[i].id = int64(i + 1)
	}
	stats.Transactions = len(txs)
	return stats, s.writeLedger(ctx, txs, names)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// transferScore weighs the evidence that an outflow and an inflow are one transfer between your
// own accounts.
func transferScore(o, n line, owners []string) int {
	od, nd := strings.ToUpper(o.Description+" "+o.Reference), strings.ToUpper(n.Description+" "+n.Reference)
	score := 0
	if ibanLike.MatchString(n.Account) && strings.Contains(strings.ReplaceAll(od, " ", ""), n.Account) {
		score += 2
	}
	if ibanLike.MatchString(o.Account) && strings.Contains(strings.ReplaceAll(nd, " ", ""), o.Account) {
		score += 2
	}
	if mentionsBank(od, n.Bank) {
		score += 2
	}
	if strings.EqualFold(n.Type, "TOPUP") || strings.Contains(nd, "TOP-UP") {
		score++
	}
	for _, owner := range owners {
		if owner != "" && (strings.Contains(od, owner) || strings.Contains(nd, owner)) {
			score++
			break
		}
	}
	return score
}

// Bank codes inside Romanian IBANs (RO49 BTRL ...), and what each bank is called on statements.
var bankCodes = map[string]string{"BTRL": "BT", "INGB": "ING", "REVO": "Revolut", "RNCB": "BCR", "BRDE": "BRD", "RZBR": "Raiffeisen", "CECE": "CEC"}

var ibanIn = regexp.MustCompile(`RO\d{2}([A-Z]{4})[A-Z0-9]{16}`)

// mentionsBank says whether a description names a bank: an IBAN at that bank, or the bank's name
// as a whole word ("REVOLUT**4321*", not the ING inside "PARKING").
func mentionsBank(desc, bank string) bool {
	for _, m := range ibanIn.FindAllStringSubmatch(strings.ReplaceAll(desc, " ", ""), -1) {
		if strings.EqualFold(bankCodes[m[1]], bank) {
			return true
		}
	}
	if len(bank) < 3 {
		return false
	}
	word := regexp.MustCompile(`(^|[^A-Z])` + regexp.QuoteMeta(strings.ToUpper(bank)) + `([^A-Z]|$)`)
	return word.MatchString(desc)
}

func (s *Store) loadLines(ctx context.Context) ([]line, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.key, b.bank, b.account, b.currency, b.seq, b.date, coalesce(b.time, ''), b.amount,
		coalesce(b.description, ''), coalesce(b.reference, ''), coalesce(b.type, ''), coalesce(b.merchant, ''), coalesce(b.merchant_key, ''),
		coalesce(l.category, '')
		FROM bank_lines b LEFT JOIN labels l ON l.key = b.key ORDER BY b.date, b.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.Key, &l.Bank, &l.Account, &l.Currency, &l.Seq, &l.Date, &l.Time, &l.Amount, &l.Description, &l.Reference,
			&l.Type, &l.Merchant, &l.MerchantKey, &l.Category); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) owners(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT DISTINCT upper(owner) FROM imports WHERE coalesce(owner, '') <> ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// assetAccounts names the ledger accounts of the bank accounts: "Assets:BT:RON", or with the last
// digits of the IBAN when one bank has several accounts.
type assetAccounts struct {
	name map[string]string // account|currency -> ledger account
	bank map[string]string // ledger account -> bank
	iban map[string]string
}

func assetNames(lines []line) assetAccounts {
	perBank := map[string]map[string]bool{}
	for _, l := range lines {
		if perBank[l.Bank] == nil {
			perBank[l.Bank] = map[string]bool{}
		}
		perBank[l.Bank][l.Account] = true
	}
	a := assetAccounts{name: map[string]string{}, bank: map[string]string{}, iban: map[string]string{}}
	for _, l := range lines {
		k := l.Account + "|" + l.Currency
		if _, ok := a.name[k]; ok {
			continue
		}
		label := l.Bank
		if len(perBank[l.Bank]) > 1 && len(l.Account) > 4 {
			label += " " + l.Account[len(l.Account)-4:]
		}
		n := "Assets:" + label + ":" + l.Currency
		a.name[k], a.bank[n] = n, l.Bank
		if ibanLike.MatchString(l.Account) {
			a.iban[n] = l.Account
		}
	}
	return a
}

func (a assetAccounts) of(account, currency string) string {
	if n, ok := a.name[account+"|"+currency]; ok {
		return n
	}
	// An opening balance for an account without lines (an empty statement).
	return "Assets:" + account + ":" + currency
}

func (s *Store) writeLedger(ctx context.Context, txs []ledgerTx, names assetAccounts) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"ledger_postings", "ledger_transactions", "ledger_accounts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return err
		}
	}
	accounts := map[string]bool{}
	var txRows, postRows, acctRows [][]any
	for _, t := range txs {
		txRows = append(txRows, []any{t.id, t.date, t.kind, t.category, t.merchant, t.description})
		for _, p := range t.postings {
			var lineKey any
			if p.lineKey != "" {
				lineKey = p.lineKey
			}
			postRows = append(postRows, []any{t.id, p.account, p.currency, p.amount, lineKey})
			if accounts[p.account] {
				continue
			}
			accounts[p.account] = true
			typ, bank, iban, cur := "equity", "", "", p.currency
			switch {
			case strings.HasPrefix(p.account, "Assets:"):
				typ, bank, iban = "asset", names.bank[p.account], names.iban[p.account]
				if bank == "" {
					bank = strings.Split(p.account, ":")[1]
				}
			case strings.HasPrefix(p.account, "Expenses:"):
				typ, cur = "expense", ""
			case strings.HasPrefix(p.account, "Income:"):
				typ, cur = "income", ""
			case p.account == transitAccount:
				typ = "transit"
			}
			acctRows = append(acctRows, []any{p.account, typ, bank, iban, cur})
		}
	}
	for _, b := range []struct {
		table string
		cols  []string
		rows  [][]any
	}{
		{"ledger_transactions", []string{"id", "date", "kind", "category", "merchant", "description"}, txRows},
		{"ledger_postings", []string{"tx_id", "account", "currency", "amount", "line_key"}, postRows},
		{"ledger_accounts", []string{"name", "type", "bank", "iban", "currency"}, acctRows},
	} {
		if _, err := bulkInsert(ctx, tx, b.table, b.cols, b.rows, ""); err != nil {
			return fmt.Errorf("%s: %w", b.table, err)
		}
	}
	return tx.Commit()
}

// displayMerchant is the merchant as questions and reports show it: the normalized key (one name
// for every store of a chain), or the raw name when there is no key.
func displayMerchant(l line) string {
	if l.MerchantKey != "" {
		return l.MerchantKey
	}
	return l.Merchant
}
