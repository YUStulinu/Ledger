// One fixed color per category, so a category looks the same on every chart and badge.
const palette: Record<string, string> = {
  Groceries: "#2f9e6e",
  Restaurants: "#e07a3f",
  "Food Delivery": "#f2b33d",
  Transport: "#3b82c4",
  Fuel: "#5b5fc7",
  Rent: "#8a5cc2",
  Utilities: "#2aa3b3",
  "Phone & Internet": "#4bb3e0",
  Subscriptions: "#d1508a",
  Health: "#d94f4f",
  Shopping: "#c78a3b",
  Entertainment: "#a66bd6",
  Travel: "#1f8a8a",
  Fees: "#7a7a7a",
  Cash: "#9a8f6b",
  Salary: "#2f9e6e",
  "Other Income": "#6cbf8a",
  Transfer: "#8c9aa8",
  Exchange: "#8c9aa8",
  Uncategorized: "#b0b0b0",
};

export function color(category: string): string {
  return palette[category] ?? "#9aa3ad";
}
