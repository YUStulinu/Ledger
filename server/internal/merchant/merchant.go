// Package merchant pulls the merchant (or the other party) out of a bank's transaction description,
// and reduces it to a key that is the same for every visit: "PROFI 2643 BUCURESTI RO" and
// "PROFI 0817 CLUJ NAPOCA RO" are both PROFI. The categorizer learns per key, so one correction
// covers every store of a chain.
package merchant

import (
	"regexp"
	"strings"
)

var (
	posPrefix  = regexp.MustCompile(`(?i)^POS\s+\d{2}/\d{2}/\d{4}\s+`)
	tid        = regexp.MustCompile(`(?i)\bTID:\S+\s*`)
	country    = regexp.MustCompile(`\s+[A-Z]{2}$`)
	cardMask   = regexp.MustCompile(`[*X]{2,}\d{2,4}\*?|\*\d{4}`)
	digits     = regexp.MustCompile(`\d{3,}`)
	legal      = regexp.MustCompile(`\b(S\.?R\.?L\.?|S\.?A\.?|SRL|SA|SCS|SNC|LTD|GMBH|INC|BV)\b\.?`)
	punct      = regexp.MustCompile(`[^A-Z0-9&.*/ ]+`)
	spaces     = regexp.MustCompile(`\s+`)
	trailingID = regexp.MustCompile(`[/*.\s]+[A-Z0-9]*\d[A-Z0-9]*$`)
)

// Cities that appear after the merchant on Romanian card statements. When a description ends in a
// country code, the word before it is the city and is dropped; two-word cities are listed here.
var twoWordCities = []string{"CLUJ NAPOCA", "TARGU MURES", "BAIA MARE", "SATU MARE", "PIATRA NEAMT", "RAMNICU VALCEA", "DROBETA TURNU"}

// Extract returns the part of a description that names the merchant or the other party.
func Extract(bank, description string) string {
	d := strings.TrimSpace(description)
	switch strings.ToUpper(bank) {
	case "BT":
		return extractBT(d)
	case "ING":
		return extractING(d)
	default: // Revolut and anything else: the description is the merchant
		return d
	}
}

func extractBT(d string) string {
	parts := strings.Split(d, ";")
	kind := strings.ToLower(strings.TrimSpace(parts[0]))
	if len(parts) < 2 {
		// The PDF statement joins the fields with spaces.
		for _, p := range []string{"plata la pos non-bt cu card visa ", "plata la pos cu card visa ", "plata online cu card visa "} {
			if strings.HasPrefix(strings.ToLower(d), p) {
				rest := d[len(p):]
				if i := strings.Index(strings.ToLower(rest), "valoare tranzactie"); i >= 0 {
					rest = rest[:i]
				}
				return dropPlace(tid.ReplaceAllString(posPrefix.ReplaceAllString(strings.TrimSpace(rest), ""), ""))
			}
		}
		return d
	}
	field := strings.TrimSpace(parts[1])
	switch {
	case strings.HasPrefix(kind, "plata la pos"), strings.HasPrefix(kind, "plata online"), strings.HasPrefix(kind, "retragere numerar"),
		strings.HasPrefix(kind, "incasare instant"):
		field = posPrefix.ReplaceAllString(field, "")
		field = tid.ReplaceAllString(field, "")
		return dropPlace(field)
	case strings.Contains(kind, " op"), strings.HasPrefix(kind, "incasare"), strings.HasPrefix(kind, "plata"):
		return field // the other party's name
	}
	return d
}

func extractING(d string) string {
	for _, part := range strings.Split(d, ";") {
		p := strings.TrimSpace(part)
		for _, label := range []string{"Terminal:", "Beneficiar:", "Ordonator:"} {
			if strings.HasPrefix(p, label) {
				v := strings.TrimSpace(p[len(label):])
				if label == "Terminal:" {
					return dropPlace(v)
				}
				return v
			}
		}
	}
	first := strings.TrimSpace(strings.Split(d, ";")[0])
	return first
}

// dropPlace removes a trailing "CITY CC" (city and two-letter country code).
func dropPlace(s string) string {
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	if !country.MatchString(up) {
		return s
	}
	s = strings.TrimSpace(s[:len(s)-3])
	up = strings.ToUpper(s)
	for _, c := range twoWordCities {
		if strings.HasSuffix(up, " "+c) {
			return strings.TrimSpace(s[:len(s)-len(c)])
		}
	}
	if i := strings.LastIndex(s, " "); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Key normalizes a merchant name for matching: upper case, no store numbers, card masks, order
// ids or legal suffixes.
func Key(name string) string {
	k := strings.ToUpper(strings.TrimSpace(name))
	k = cardMask.ReplaceAllString(k, " ")
	k = legal.ReplaceAllString(k, " ")
	k = punct.ReplaceAllString(k, " ")
	k = spaces.ReplaceAllString(strings.TrimSpace(k), " ")
	// "BOLT.EU/R/2504011717" -> "BOLT.EU/R", "KAUFLAND 2091" -> "KAUFLAND".
	for {
		next := strings.TrimSpace(trailingID.ReplaceAllString(k, ""))
		if next == k || next == "" {
			break
		}
		k = next
	}
	k = digits.ReplaceAllString(k, "")
	k = strings.Trim(spaces.ReplaceAllString(k, " "), " ./*")
	if k == "" {
		return strings.ToUpper(strings.TrimSpace(name))
	}
	return k
}
