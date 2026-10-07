import type { CardPrice, CollectionCard } from "@/types";

// Mirrors copyValue in prices.go: the non-English product for a copy in
// another language when the card has one, else the English (or only)
// product; the foil column for a foil copy when the product has one, else
// the normal column. null when the copy has no price.
export function copyValue(copy: Pick<CollectionCard, "foil" | "lang">, price: CardPrice | undefined): number | null {
  if (!price) return null;
  let trend = price.trend;
  let foil = price.trend_foil;
  if (copy.lang !== "en" && (price.trend_non_english > 0 || price.trend_non_english_foil > 0)) {
    trend = price.trend_non_english;
    foil = price.trend_non_english_foil;
  }
  if (copy.foil && foil > 0) return foil;
  if (trend > 0) return trend;
  return null;
}

export interface ValueSum {
  value: number;
  priced: number; // copies with a price
  copies: number;
}

export function sumValues(copies: CollectionCard[], prices: Record<string, CardPrice>): ValueSum {
  const out: ValueSum = { value: 0, priced: 0, copies: copies.length };
  for (const c of copies) {
    const v = copyValue(c, prices[c.card_id]);
    if (v !== null) {
      out.value += v;
      out.priced++;
    }
  }
  return out;
}

const eur = new Intl.NumberFormat("en", { style: "currency", currency: "EUR" });

// "€2.32"; amounts under a cent show as "<€0.01" rather than rounding to zero.
export function formatEUR(v: number): string {
  if (v > 0 && v < 0.005) return "<€0.01";
  return eur.format(v);
}

// "7 Oct" from an ISO date; the raw string when it does not parse.
export function formatGuideDate(iso: string): string {
  const d = new Date(iso.length === 10 ? iso + "T00:00:00" : iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-GB", { day: "numeric", month: "short" });
}

// The footer wording for a total: "€1,204.50 · 108 of 110 cards priced",
// or just the amount when every copy is priced.
export function describeTotal(s: ValueSum): string {
  if (s.priced === 0) return "";
  const amount = formatEUR(s.value);
  if (s.priced === s.copies) return amount;
  return `${amount} · ${s.priced.toLocaleString("en-US")} of ${s.copies.toLocaleString("en-US")} cards priced`;
}
