package importer

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// ofx reads OFX and QFX files (Open Financial Exchange: what US, Canadian and many other banks and
// Quicken export), both the old SGML form (tags without closing tags) and the XML form.
//
// An OFX statement gives the balance at its end (LEDGERBAL) but not at its start, so the rows
// cannot be checked against it; the opening balance is worked out from it instead.
type ofx struct{}

func (ofx) Name() string { return "ofx" }

func (ofx) Detect(name string, data []byte) bool {
	s := strings.ToUpper(string(head(data)))
	n := strings.ToLower(name)
	return strings.Contains(s, "<OFX>") || strings.Contains(s, "OFXHEADER") || strings.HasSuffix(n, ".ofx") || strings.HasSuffix(n, ".qfx")
}

// ofxField reads <TAG>value from SGML or XML OFX.
func ofxField(block, tag string) string {
	re := regexp.MustCompile(`(?is)<` + tag + `>([^<\r\n]*)`)
	if m := re.FindStringSubmatch(block); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ofxDate reads 20260305, 20260305120000 or 20260305120000.000[-5:EST].
func ofxDate(s string) (time.Time, error) {
	if len(s) < 8 {
		return time.Time{}, fmt.Errorf("not a date: %q", s)
	}
	t, err := time.Parse("20060102", s[:8])
	return time0(t), err
}

func (ofx) Parse(data []byte) (*Statement, error) {
	s := text(data)
	// Each <STMTTRN> block; the SGML form may leave them unclosed, so split on the opening tags.
	parts := strings.Split(s, "<STMTTRN>")
	if len(parts) < 2 {
		parts = strings.Split(strings.ReplaceAll(s, "<stmttrn>", "<STMTTRN>"), "<STMTTRN>")
	}
	if len(parts) < 2 {
		return nil, fmt.Errorf("no transactions (STMTTRN) found")
	}
	cur := strings.ToUpper(ofxField(s, "CURDEF"))
	account := ofxField(s, "ACCTID")
	bank := ofxField(s, "ORG")
	if bank == "" {
		bank = "Bank"
	}
	if account == "" {
		account = bank
	}
	st := &Statement{Bank: bank, Format: "ofx", Account: account, Currency: cur, Opening: map[string]money.Amount{}, Closing: map[string]money.Amount{}}
	for i, p := range parts[1:] {
		if end := strings.Index(strings.ToUpper(p), "</STMTTRN>"); end >= 0 {
			p = p[:end]
		}
		d, err := ofxDate(ofxField(p, "DTPOSTED"))
		if err != nil {
			return nil, fmt.Errorf("transaction %d: %w", i+1, err)
		}
		amt, err := money.Parse(ofxField(p, "TRNAMT"))
		if err != nil {
			return nil, fmt.Errorf("transaction %d: %w", i+1, err)
		}
		name, memo := ofxField(p, "NAME"), ofxField(p, "MEMO")
		desc := name
		if memo != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(memo)) {
			desc = strings.TrimSpace(name + " " + memo)
		}
		c := strings.ToUpper(ofxField(p, "CURRENCY"))
		if c == "" {
			c = cur
		}
		st.Rows = append(st.Rows, Row{Line: i + 1, Date: d, Description: desc, Amount: amt, Currency: c,
			Reference: ofxField(p, "FITID"), Type: strings.ToUpper(ofxField(p, "TRNTYPE"))})
	}
	if cur == "" {
		return nil, fmt.Errorf("the file does not say its currency (CURDEF)")
	}
	sortStable(st.Rows)
	// The closing balance, and the opening one worked out from it.
	if lb := regexp.MustCompile(`(?is)<LEDGERBAL>(.*?)(?:</LEDGERBAL>|<AVAILBAL>|$)`).FindStringSubmatch(s); lb != nil {
		if v, err := money.Parse(ofxField(lb[1], "BALAMT")); err == nil {
			var sum money.Amount
			for _, r := range st.Rows {
				if r.Currency == cur {
					sum += r.Amount
				}
			}
			st.Closing[cur] = v
			st.Opening[cur] = v - sum
		}
	}
	st.Warnings = append(st.Warnings, "OFX gives only the closing balance, so the rows could not be checked against a running balance; the opening balance was worked out from it")
	return st, nil
}
