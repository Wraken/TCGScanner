import { Camera, FolderOpen, ImageIcon } from "lucide-react";
import { gameLabel } from "@/lib/utils";
import { formatEUR, formatGuideDate } from "@/lib/prices";
import type { Collection, GameCollection, GameValue, PricesStatus } from "@/types";
import pokemonCover from "@/assets/games/pokemon.svg";
import onepieceCover from "@/assets/games/onepiece.svg";
import lorcanaCover from "@/assets/games/lorcana.svg";
import riftboundCover from "@/assets/games/riftbound.svg";

// Original in-house artwork (no trademarked logos: the repo is public).
// Unknown games and the "Other" bucket fall back to the icon placeholder.
const GAME_COVERS: Record<string, string> = {
  pokemon: pokemonCover,
  onepiece: onepieceCover,
  lorcana: lorcanaCover,
  riftbound: riftboundCover,
};

interface Props {
  games: GameCollection[];
  customs: Collection[];
  // Bundle cover art per game, cached app-wide by useCollection.
  covers: Record<string, string>;
  // Cardmarket prices: per-game totals, and the facts behind the status line.
  values: Record<string, GameValue>;
  prices: PricesStatus | null;
  onRefreshPrices: () => void;
  onTogglePrices: (enabled: boolean) => void;
  onScan: () => void;
  onOpenGame: (game: string) => void;
  onOpenCustom: (id: number) => void;
}

function fmt(n: number): string {
  return n.toLocaleString("en-US");
}

function cards(n: number): string {
  return `${fmt(n)} ${n === 1 ? "card" : "cards"}`;
}

// The one quiet line about prices: off, fetching, missing or old, else the
// guides' date. Refresh and the on/off switch live here, since there is no
// settings screen yet.
function PricesLine({ prices, onRefresh, onToggle }: { prices: PricesStatus; onRefresh: () => void; onToggle: (on: boolean) => void }) {
  if (!prices.available) return <p className="text-xs n-n500 m-0">This model bundle has no price data.</p>;
  const link = (label: string, onClick: () => void, disabled = false) => (
    <button type="button" className="n-btn n-btn-link" onClick={onClick} disabled={disabled}>{label}</button>
  );
  if (!prices.enabled) {
    return (
      <p className="text-xs n-n500 m-0 flex items-center gap-1">
        Prices off {link("Turn on", () => onToggle(true))}
      </p>
    );
  }
  const games = Object.entries(prices.games);
  const missing = games.filter(([, g]) => !g.as_of).map(([game]) => gameLabel(game));
  const failed = games.filter(([, g]) => g.error).map(([game]) => gameLabel(game));
  const dates = games.map(([, g]) => g.as_of).filter(Boolean).sort();
  let text: string;
  if (prices.refreshing) text = "Fetching today's prices…";
  else if (dates.length === 0) text = "No prices yet";
  else if (missing.length > 0) text = `Prices from ${formatGuideDate(dates[0])} · none for ${missing.join(", ")}`;
  else text = `${prices.stale ? "Prices from" : "Cardmarket prices from"} ${formatGuideDate(dates[0])}`;
  if (failed.length > 0 && !prices.refreshing) text += ` · last refresh failed for ${failed.join(", ")}`;
  return (
    <p className="text-xs n-n500 m-0 flex items-center gap-1 flex-wrap" aria-live="polite">
      {text}
      {!prices.refreshing && link("Refresh", onRefresh)}
      {link("Turn off", () => onToggle(false))}
    </p>
  );
}

export function HomeScreen({
  games, customs, covers, values, prices, onRefreshPrices, onTogglePrices, onScan, onOpenGame, onOpenCustom,
}: Props) {
  const totalCards = games.reduce((s, g) => s + g.card_count, 0);
  const trackedGames = games.filter((g) => g.game !== "").length;
  const worth = Object.values(values).reduce(
    (s, v) => ({ value: s.value + v.value, priced: s.priced + v.priced }),
    { value: 0, priced: 0 }
  );

  const stats = [
    { value: fmt(totalCards), label: "Cards" },
    { value: fmt(trackedGames), label: "Games tracked" },
    { value: fmt(customs.length), label: "Custom collections" },
  ];
  if (worth.priced > 0) {
    stats.push({
      value: formatEUR(worth.value),
      label: worth.priced === totalCards ? "Collection value" : `Value · ${fmt(worth.priced)} of ${fmt(totalCards)} priced`,
    });
  }

  return (
    <div className="h-full overflow-y-auto" style={{ padding: "36px 44px" }}>
      <div className="flex flex-col" style={{ gap: "26px" }}>
        {/* Header */}
        <div className="flex items-start justify-between gap-4">
          <h1 className="text-[32px] leading-tight">Your collection</h1>
          <button onClick={onScan} className="n-btn n-btn-primary shrink-0">
            <Camera className="w-4 h-4" aria-hidden="true" />
            Scan cards
          </button>
        </div>

        {/* Stats band */}
        <dl className="flex flex-wrap gap-x-16 gap-y-6 m-0">
          {stats.map((s) => (
            <div key={s.label} className="flex flex-col-reverse">
              <dt
                className="mt-2 n-n400"
                style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
              >
                {s.label}
              </dt>
              <dd className="m-0 text-[40px] font-medium leading-none tabular-nums">{s.value}</dd>
            </div>
          ))}
        </dl>

        {prices && <PricesLine prices={prices} onRefresh={onRefreshPrices} onToggle={onTogglePrices} />}

        <hr className="n-hr" />

        {/* Game tiles: as many columns as fit, so names never wrap at the
            1024px window size. */}
        <div className="grid gap-[18px]" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(260px, 1fr))" }}>
          {games.map((g) => {
            const cover = covers[g.game] ?? GAME_COVERS[g.game];
            return (
            <button
              key={g.game || "__other"}
              type="button"
              className="n-tile w-full"
              style={{ textAlign: "inherit", padding: 0 }}
              onClick={() => onOpenGame(g.game)}
            >
              <div
                className="flex flex-col items-center justify-center gap-2 overflow-hidden"
                style={{ height: "280px", background: "var(--n-bg)" }}
              >
                {cover ? (
                  <img src={cover} alt="" className="w-full h-full object-cover" />
                ) : (
                  <ImageIcon className="w-6 h-6 n-n600" aria-hidden="true" />
                )}
              </div>
              <div style={{ padding: "14px 16px 16px" }} className="flex items-baseline justify-between gap-3">
                <span className="text-base font-medium truncate min-w-0">{gameLabel(g.game)}</span>
                <span className="flex flex-col items-end text-xs n-n400 whitespace-nowrap tabular-nums">
                  <span>{cards(g.card_count)}</span>
                  {(values[g.game]?.priced ?? 0) > 0 && (
                    <span className="n-n500">{formatEUR(values[g.game].value)}</span>
                  )}
                </span>
              </div>
            </button>
            );
          })}
        </div>

        {/* Custom collections */}
        {customs.length > 0 && (
          <>
            <hr className="n-hr" />
            <section className="flex flex-col gap-2" aria-labelledby="home-customs">
              <h2
                id="home-customs"
                className="n-n400"
                style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
              >
                Custom collections
              </h2>
              <div className="flex flex-wrap gap-2">
                {customs.map((c) => (
                  <button
                    key={c.id}
                    className="n-btn n-btn-secondary max-w-[320px]"
                    title={c.name}
                    onClick={() => onOpenCustom(c.id)}
                  >
                    <FolderOpen className="w-4 h-4 shrink-0" aria-hidden="true" />
                    <span className="truncate">{c.name}</span>
                    <span className="n-n500 text-xs tabular-nums">({c.card_count})</span>
                  </button>
                ))}
              </div>
            </section>
          </>
        )}
      </div>
    </div>
  );
}
