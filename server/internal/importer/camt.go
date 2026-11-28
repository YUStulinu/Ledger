package importer

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// camt053 reads ISO 20022 camt.053 bank-to-customer statements (XML), which banks across Europe
// (and many elsewhere) offer for download, any version of the schema. A camt.053 statement states
// its opening and closing balances, so every file is checked: opening plus the entries must give
// the closing balance.
type camt053 struct{}

func (camt053) Name() string { return "camt053" }

func (camt053) Detect(name string, data []byte) bool {
	s := string(head(data))
	return strings.Contains(s, "camt.053") || strings.Contains(s, "BkToCstmrStmt")
}

type camtAmt struct {
	Ccy   string `xml:"Ccy,attr"`
	Value string `xml:",chardata"`
}

type camtDate struct {
	Dt   string `xml:"Dt"`
	DtTm string `xml:"DtTm"`
}

func (d camtDate) time() (time.Time, error) {
	s := d.Dt
	if s == "" && len(d.DtTm) >= 10 {
		s = d.DtTm[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	return time0(t), err
}

type camtParty struct {
	Nm  string `xml:"Nm"`
	Pty struct {
		Nm string `xml:"Nm"`
	} `xml:"Pty"`
}

func (p camtParty) name() string {
	if p.Nm != "" {
		return p.Nm
	}
	return p.Pty.Nm
}

type camtDoc struct {
	Stmts []struct {
		Acct struct {
			ID struct {
				IBAN string `xml:"IBAN"`
				Othr struct {
					ID string `xml:"Id"`
				} `xml:"Othr"`
			} `xml:"Id"`
			Ccy  string `xml:"Ccy"`
			Svcr struct {
				FinInstnId struct {
					Nm  string `xml:"Nm"`
					BIC string `xml:"BIC"`
				} `xml:"FinInstnId"`
			} `xml:"Svcr"`
		} `xml:"Acct"`
		Bals []struct {
			Tp struct {
				CdOrPrtry struct {
					Cd string `xml:"Cd"`
				} `xml:"CdOrPrtry"`
			} `xml:"Tp"`
			Amt       camtAmt  `xml:"Amt"`
			CdtDbtInd string   `xml:"CdtDbtInd"`
			Dt        camtDate `xml:"Dt"`
		} `xml:"Bal"`
		Ntries []struct {
			Amt       camtAmt `xml:"Amt"`
			CdtDbtInd string  `xml:"CdtDbtInd"`
			Sts       struct {
				Cd    string `xml:"Cd"`
				Value string `xml:",chardata"`
			} `xml:"Sts"`
			BookgDt      camtDate `xml:"BookgDt"`
			ValDt        camtDate `xml:"ValDt"`
			AcctSvcrRef  string   `xml:"AcctSvcrRef"`
			AddtlNtryInf string   `xml:"AddtlNtryInf"`
			Dtls         []struct {
				TxDtls []struct {
					RltdPties struct {
						Cdtr camtParty `xml:"Cdtr"`
						Dbtr camtParty `xml:"Dbtr"`
					} `xml:"RltdPties"`
					RmtInf struct {
						Ustrd []string `xml:"Ustrd"`
					} `xml:"RmtInf"`
					AddtlTxInf string `xml:"AddtlTxInf"`
				} `xml:"TxDtls"`
			} `xml:"NtryDtls"`
		} `xml:"Ntry"`
	} `xml:"BkToCstmrStmt>Stmt"`
}

func signed(a camtAmt, ind string) (money.Amount, error) {
	v, err := money.Parse(a.Value)
	if err != nil {
		return 0, err
	}
	v = abs(v)
	if strings.EqualFold(ind, "DBIT") {
		v = -v
	}
	return v, nil
}

func (camt053) Parse(data []byte) (*Statement, error) {
	var doc camtDoc
	if err := xml.Unmarshal([]byte(text(data)), &doc); err != nil {
		return nil, fmt.Errorf("camt.053: %w", err)
	}
	if len(doc.Stmts) == 0 {
		return nil, fmt.Errorf("camt.053: no statement (Stmt) found")
	}
	first := doc.Stmts[0]
	account := first.Acct.ID.IBAN
	if account == "" {
		account = first.Acct.ID.Othr.ID
	}
	bank := first.Acct.Svcr.FinInstnId.Nm
	if bank == "" {
		bank = first.Acct.Svcr.FinInstnId.BIC
	}
	if bank == "" {
		bank = "Bank"
	}
	st := &Statement{Bank: bank, Format: "camt053", Account: account, Currency: strings.ToUpper(first.Acct.Ccy),
		Opening: map[string]money.Amount{}, Closing: map[string]money.Amount{}}
	line := 0
	for si, stmt := range doc.Stmts {
		if id := stmt.Acct.ID.IBAN; id != "" && id != account {
			st.Warnings = append(st.Warnings, fmt.Sprintf("statement %d is for another account (%s) and was left out", si+1, id))
			continue
		}
		for _, b := range stmt.Bals {
			v, err := signed(b.Amt, b.CdtDbtInd)
			if err != nil {
				return nil, fmt.Errorf("statement %d balance: %w", si+1, err)
			}
			cur := strings.ToUpper(b.Amt.Ccy)
			switch b.Tp.CdOrPrtry.Cd {
			case "OPBD", "PRCD":
				if _, ok := st.Opening[cur]; !ok { // the first statement's opening
					st.Opening[cur] = v
				}
			case "CLBD":
				st.Closing[cur] = v // the last statement's closing
			}
		}
		for _, e := range stmt.Ntries {
			line++
			status := strings.ToUpper(strings.TrimSpace(e.Sts.Cd + e.Sts.Value))
			if status != "" && status != "BOOK" {
				continue // pending or informational entries
			}
			v, err := signed(e.Amt, e.CdtDbtInd)
			if err != nil {
				return nil, fmt.Errorf("entry %d: %w", line, err)
			}
			d, err := e.BookgDt.time()
			if err != nil {
				if d, err = e.ValDt.time(); err != nil {
					return nil, fmt.Errorf("entry %d: no booking date", line)
				}
			}
			var parts []string
			for _, dt := range e.Dtls {
				for _, tx := range dt.TxDtls {
					party := tx.RltdPties.Cdtr.name()
					if strings.EqualFold(e.CdtDbtInd, "CRDT") {
						party = tx.RltdPties.Dbtr.name()
					}
					if party != "" {
						parts = append(parts, party)
					}
					parts = append(parts, tx.RmtInf.Ustrd...)
					if tx.AddtlTxInf != "" {
						parts = append(parts, tx.AddtlTxInf)
					}
				}
			}
			if len(parts) == 0 && e.AddtlNtryInf != "" {
				parts = append(parts, e.AddtlNtryInf)
			}
			st.Rows = append(st.Rows, Row{Line: line, Date: d, Description: strings.Join(strings.Fields(strings.Join(parts, " ")), " "),
				Amount: v, Currency: strings.ToUpper(e.Amt.Ccy), Reference: e.AcctSvcrRef})
		}
	}
	if st.Currency == "" && len(st.Rows) > 0 {
		st.Currency = st.Rows[0].Currency
	}
	if st.Account == "" {
		st.Account = bank
	}
	sortStable(st.Rows)
	if len(st.Opening) == 0 || len(st.Closing) == 0 {
		st.Warnings = append(st.Warnings, "the statement lacks an opening or a closing balance, so it could not be checked")
	}
	return st, nil
}
