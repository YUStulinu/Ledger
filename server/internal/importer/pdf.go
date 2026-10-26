package importer

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// btPDF reads a BT statement printed as a PDF. A PDF has no columns, only text placed on the page,
// so the parser rebuilds the lines from the glyphs' positions, and decides whether an amount is a
// debit or a credit by where it sits: under the "Debit" heading or under "Credit". The statement's
// SOLD ANTERIOR and SOLD FINAL lines give the balances the import is checked against.
type btPDF struct{}

func (btPDF) Name() string { return "bt-pdf" }

func (btPDF) Detect(name string, data []byte) bool {
	return bytes.HasPrefix(data, []byte("%PDF"))
}

// A glyph run on one line of the page.
type piece struct {
	x0, x1 float64 // start and end
	s      string
}

type pdfLine struct {
	y      float64
	pieces []piece
}

func (l pdfLine) text() string {
	var b strings.Builder
	for _, p := range l.pieces {
		b.WriteString(p.s)
	}
	return b.String()
}

// How far (in points) an amount may end from its column's heading: about two characters of a
// statement's font.
const columnSlack = 12.0

var (
	amountRe = regexp.MustCompile(`\d{1,3}(?:\.\d{3})*,\d{2}`)
	dateRe   = regexp.MustCompile(`^\s*(\d{2}\.\d{2}\.\d{4})\s+(.*)$`)
	ibanRe   = regexp.MustCompile(`RO\d{2}[A-Z]{4}[A-Z0-9]{16}`)
)

func (btPDF) Parse(data []byte) (*Statement, error) {
	lines, err := pdfLines(data)
	if err != nil {
		return nil, err
	}
	st := &Statement{Bank: "BT", Format: "bt-pdf", Account: "BT", Currency: "RON", Opening: map[string]money.Amount{}, Closing: map[string]money.Amount{}}
	debitX, creditX := math.NaN(), math.NaN()
	for i, l := range lines {
		s := l.text()
		up := strings.ToUpper(s)
		switch {
		case ibanRe.MatchString(up) && st.Account == "BT":
			st.Account = ibanRe.FindString(up)
		case strings.HasPrefix(strings.TrimSpace(up), "TITULAR:"):
			st.Owner = strings.TrimSpace(s[strings.Index(up, "TITULAR:")+8:])
		case strings.Contains(up, "SOLD ANTERIOR"), strings.Contains(up, "SOLD FINAL"):
			if m := amountRe.FindAllString(s, -1); len(m) > 0 {
				v := money.MustParse(m[len(m)-1])
				if strings.Contains(up, "MINUS") || strings.Contains(s, "-"+m[len(m)-1]) {
					v = -v
				}
				if strings.Contains(up, "SOLD ANTERIOR") {
					st.Opening[st.Currency] = v
				} else {
					st.Closing[st.Currency] = v
				}
			}
		case strings.Contains(up, "DEBIT") && strings.Contains(up, "CREDIT"):
			// The headings are right-aligned over their columns: remember where they end.
			debitX, creditX = endOf(l, "Debit"), endOf(l, "Credit")
		default:
			if math.IsNaN(debitX) {
				continue
			}
			// Only amounts that end under a column heading are the row's amounts; others (such as
			// "valoare tranzactie: 12,30") are part of the description.
			var amounts []piece
			for _, a := range amountPieces(l) {
				if math.Abs(a.x1-debitX) < columnSlack || math.Abs(a.x1-creditX) < columnSlack {
					amounts = append(amounts, a)
				}
			}
			m := dateRe.FindStringSubmatch(s)
			if m == nil {
				// A continuation of the previous row's description.
				if t := strings.TrimSpace(s); t != "" && len(amounts) == 0 && len(st.Rows) > 0 {
					last := &st.Rows[len(st.Rows)-1]
					last.Description = strings.TrimSpace(last.Description + " " + strings.Join(strings.Fields(t), " "))
				}
				continue
			}
			date, err := time.Parse("02.01.2006", m[1])
			if err != nil {
				continue
			}
			if len(amounts) == 0 {
				return nil, fmt.Errorf("line %d (%s): no amount under Debit or Credit", i+1, strings.TrimSpace(s))
			}
			row := Row{Line: i + 1, Date: date, Currency: st.Currency}
			desc := m[2]
			for _, a := range amounts {
				v := money.MustParse(a.s)
				// Whichever column's end is nearer.
				if math.Abs(a.x1-debitX) <= math.Abs(a.x1-creditX) {
					row.Amount -= v
				} else {
					row.Amount += v
				}
				if k := strings.LastIndex(desc, a.s); k >= 0 {
					desc = desc[:k] + desc[k+len(a.s):]
				}
			}
			row.Description = strings.Join(strings.Fields(desc), " ")
			st.Rows = append(st.Rows, row)
		}
	}
	if math.IsNaN(debitX) {
		return nil, fmt.Errorf("no Debit/Credit table found in the PDF")
	}
	return st, nil
}

// pdfLines extracts the text of every page as lines (top to bottom), each a list of glyph runs with
// their horizontal extent.
func pdfLines(data []byte) ([]pdfLine, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a readable PDF: %w", err)
	}
	var out []pdfLine
	for p := 1; p <= r.NumPage(); p++ {
		page := r.Page(p)
		if page.V.IsNull() {
			continue
		}
		content := page.Content()
		// Group glyphs into lines by their baseline.
		byY := map[int][]pdf.Text{}
		for _, t := range content.Text {
			byY[int(math.Round(t.Y))] = append(byY[int(math.Round(t.Y))], t)
		}
		ys := make([]int, 0, len(byY))
		for y := range byY {
			ys = append(ys, y)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(ys)))
		for _, y := range ys {
			glyphs := byY[y]
			sort.SliceStable(glyphs, func(i, j int) bool { return glyphs[i].X < glyphs[j].X })
			var l pdfLine
			l.y = float64(y)
			// A PDF without glyph widths puts every glyph of a string at the same x: fall back to
			// character positions (statements are printed in monospaced fonts).
			flat := len(glyphs) > 1 && glyphs[len(glyphs)-1].X == glyphs[0].X && glyphs[0].W == 0
			for k, g := range glyphs {
				if flat {
					l.pieces = append(l.pieces, piece{x0: float64(k), x1: float64(k + 1), s: g.S})
				} else {
					l.pieces = append(l.pieces, piece{x0: g.X, x1: g.X + g.W, s: g.S})
				}
			}
			out = append(out, l)
		}
	}
	return out, nil
}

// charEnds spells out a line and the x where each of its characters ends. A piece may hold several
// characters (a whole string shown at once): they share its width evenly, which is exact for the
// monospaced fonts statements are printed in and close enough otherwise.
func charEnds(l pdfLine) (string, []float64) {
	s, pos := "", []float64{}
	for _, p := range l.pieces {
		n := len([]rune(p.s))
		for k := 1; k <= n; k++ {
			pos = append(pos, p.x0+(p.x1-p.x0)*float64(k)/float64(n))
		}
		s += p.s
	}
	return s, pos
}

// endOf is the x where `word` ends on the line (NaN if absent).
func endOf(l pdfLine, word string) float64 {
	s, pos := charEnds(l)
	i := strings.LastIndex(strings.ToLower(s), strings.ToLower(word))
	if i < 0 {
		return math.NaN()
	}
	return pos[len([]rune(s[:i+len(word)]))-1]
}

// amountPieces finds the amounts on a line with the x where each ends.
func amountPieces(l pdfLine) []piece {
	s, ends := charEnds(l)
	var out []piece
	for _, loc := range amountRe.FindAllStringIndex(s, -1) {
		end := len([]rune(s[:loc[1]])) - 1
		out = append(out, piece{x1: ends[end], s: s[loc[0]:loc[1]]})
	}
	return out
}
