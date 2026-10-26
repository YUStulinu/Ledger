package merchant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/YUStulinu/Ledger/server/internal/importer"
)

func TestExtractAndKey(t *testing.T) {
	cases := []struct{ bank, desc, key string }{
		{"BT", "Plata la POS non-BT cu card VISA;POS 01/04/2025 TID:26955846 PROFI 2643 BUCURESTI RO;valoare tranzactie: 159,81 RON RRN:873923542950;", "PROFI"},
		{"BT", "Plata la POS non-BT cu card VISA;POS 01/04/2025 TID:26955846 LIDL DISCOUNT 0817 CLUJ NAPOCA RO;valoare tranzactie: 9,81 RON;", "LIDL DISCOUNT"},
		{"BT", "Plata online cu card VISA;BOLT.EU/R/2504011717 BUCURESTI RO;valoare tranzactie: 53,59 RON RRN:371170675922;", "BOLT.EU/R"},
		{"BT", "Plata online cu card VISA;NETFLIX.COM AMSTERDAM RO;valoare tranzactie: 49,99 RON;", "NETFLIX.COM"},
		{"BT", "Plata OP - canal electronic;POPESCU ANDREI;RO35RNCB0082000000001234;Chirie aprilie 2025;REF: 4336370223932723", "POPESCU ANDREI"},
		{"BT", "Incasare OP - canal electronic;NORTHWIND SOFTWARE SRL;RO02BACX0000001234567890;Salariu martie 2026;REF: 1", "NORTHWIND SOFTWARE"},
		{"BT", "Retragere numerar ATM;ATM BT 4821 BUCURESTI RO;valoare tranzactie: 300,00 RON;", "ATM BT"},
		{"BT", "Comision administrare cont", "COMISION ADMINISTRARE CONT"},
		{"BT", "Plata la POS non-BT cu card VISA POS 07/09/2026 TID:71141719 MOL 6307 BUCURESTI RO valoare tranzactie: 353,28 RON RRN:634059077451", "MOL"},
		{"ING", "Cumparare POS; Nr. card: ****5678; Terminal: LIDL DISCOUNT 8301 BUCURESTI RO; Data: 14-04-2025", "LIDL DISCOUNT"},
		{"ING", "Plata debit direct; Beneficiar: ENEL ENERGIE MUNTENIA SA; Referinta: 0000000024", "ENEL ENERGIE MUNTENIA"},
		{"ING", "Incasare; Ordonator: IONESCU MARIA; Din contul: RO49BTRLRONCRT0123456701; Detalii: Economii", "IONESCU MARIA"},
		{"Revolut", "BOLT.EU/O/2504074264", "BOLT.EU/O"},
		{"Revolut", "UBER *TRIP", "UBER *TRIP"},
		{"Revolut", "Top-up by *1234", "TOP UP BY"},
		{"Revolut", "Cerveceria Catalana", "CERVECERIA CATALANA"},
		{"BT", "Plata la POS non-BT cu card VISA;POS 05/03/2026 TID:1 H&M 0123 BUCURESTI RO;", "H&M"},
	}
	for _, c := range cases {
		if got := Key(Extract(c.bank, c.desc)); got != c.key {
			t.Errorf("%s %q: key %q, want %q (extracted %q)", c.bank, c.desc, got, c.key, Extract(c.bank, c.desc))
		}
	}
}

// Every store of a chain gets one key, and different chains different keys, across the synthetic
// statements of all three banks.
func TestKeysAreStableAcrossStatements(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "testdata", "synthetic")
	var truth struct {
		Transactions []struct {
			Account  string `json:"account"`
			Merchant string `json:"merchant"`
			Date     string `json:"date"`
			Amount   int64  `json:"amount"`
		} `json:"transactions"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "truth.json"))
	if err != nil {
		t.Skip(err)
	}
	if err := json.Unmarshal(data, &truth); err != nil {
		t.Fatal(err)
	}
	keysOf := map[string]map[string]bool{} // true merchant -> keys seen
	merchantsOf := map[string]map[string]bool{}
	files := map[string]string{"bt": "bt_2025-04_2026-09.csv", "ing": "ing_2025-04_2025-12.csv", "revolut": "revolut_2025-04_2026-09.csv"}
	for account, file := range files {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		st, err := importer.Read(file, raw)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, tx := range truth.Transactions {
			if tx.Account == account && (account != "ing" || tx.Date <= "2025-12-31") {
				want = append(want, tx.Merchant)
			}
		}
		for i, r := range st.Rows {
			k, m := Key(Extract(st.Bank, r.Description)), want[i]
			if keysOf[m] == nil {
				keysOf[m] = map[string]bool{}
			}
			if merchantsOf[k] == nil {
				merchantsOf[k] = map[string]bool{}
			}
			keysOf[m][account+":"+k] = true
			merchantsOf[k][m] = true
		}
	}
	for m, keys := range keysOf {
		// Not merchants: the bank's fees are different charges with their own texts, and the two
		// legs of a currency exchange are described differently.
		if m == "Banca Transilvania" || m == "Revolut exchange" {
			continue
		}
		perBank := map[string]int{}
		for k := range keys {
			perBank[k[:3]]++
		}
		for bank, n := range perBank {
			if n > 1 {
				t.Errorf("%s has %d keys at %s: %v", m, n, bank, keys)
			}
		}
	}
	for k, ms := range merchantsOf {
		if len(ms) > 1 && k != "IONESCU MARIA" { // the owner appears on both sides of her own transfers
			t.Errorf("key %q covers several merchants: %v", k, ms)
		}
	}
}
