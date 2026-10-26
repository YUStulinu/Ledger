// Package category is the fixed set of categories transactions are sorted into. The classifier,
// the language model's constrained output, the SQL views and the UI all use this one list.
package category

// Kind says which side of the books a category is on.
type Kind string

const (
	Expense Kind = "expense"
	Income  Kind = "income"
	// Neutral: money moving between your own accounts or currencies, neither earned nor spent.
	Neutral Kind = "neutral"
)

type Category struct {
	Name string
	Kind Kind
	// What belongs there, for the language model's prompt and the UI.
	Hint string
}

const (
	Uncategorized = "Uncategorized"
	Transfer      = "Transfer"
	Exchange      = "Exchange"
)

// All lists the categories in display order.
var All = []Category{
	{"Groceries", Expense, "supermarkets and food shops (Kaufland, Lidl, Mega Image, Carrefour, Profi)"},
	{"Restaurants", Expense, "restaurants, cafes, bars, fast food eaten out"},
	{"Food Delivery", Expense, "food ordered through apps (Glovo, Tazz, Bolt Food)"},
	{"Transport", Expense, "taxis and ride hailing (Bolt, Uber), public transport, metro, parking"},
	{"Fuel", Expense, "petrol stations (OMV, Petrom, MOL, Rompetrol)"},
	{"Rent", Expense, "rent paid to a landlord"},
	{"Utilities", Expense, "electricity, gas, water, heating (Enel, Engie, E.ON)"},
	{"Phone & Internet", Expense, "mobile phone, internet, TV (Digi, Orange, Vodafone)"},
	{"Subscriptions", Expense, "recurring digital services and memberships (Netflix, Spotify, Dropbox, gym)"},
	{"Health", Expense, "pharmacies, doctors, dentists, clinics"},
	{"Shopping", Expense, "clothes, electronics, home goods, online shops (eMAG, Altex, Decathlon, H&M)"},
	{"Entertainment", Expense, "cinema, concerts, events, games"},
	{"Travel", Expense, "flights, hotels, trains, holidays (Wizz Air, Booking.com)"},
	{"Fees", Expense, "bank fees and commissions"},
	{"Cash", Expense, "cash withdrawals at ATMs"},
	{"Salary", Income, "salary and wages"},
	{"Other Income", Income, "interest, refunds from the state, other money received"},
	{Transfer, Neutral, "money moved between your own accounts (top-ups, savings)"},
	{Exchange, Neutral, "currency exchange between your own pockets"},
	{Uncategorized, Expense, "not sorted yet"},
}

var byName = func() map[string]Category {
	m := map[string]Category{}
	for _, c := range All {
		m[c.Name] = c
	}
	return m
}()

// Lookup returns a category by name.
func Lookup(name string) (Category, bool) {
	c, ok := byName[name]
	return c, ok
}

// Names lists every category name.
func Names() []string {
	out := make([]string, len(All))
	for i, c := range All {
		out[i] = c.Name
	}
	return out
}

// Assignable lists the categories a transaction can be given by hand or by a model. Exchange comes
// only from pairing an exchange's two legs, and Uncategorized is the absence of a category; Transfer
// stays, for money sent to an account of yours whose statements are not imported.
func Assignable() []string {
	var out []string
	for _, c := range All {
		if c.Name != Exchange && c.Name != Uncategorized {
			out = append(out, c.Name)
		}
	}
	return out
}
