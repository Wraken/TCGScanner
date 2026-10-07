import { Camera, FolderOpen, ImageIcon } from "lucide-react";
import { gameLabel } from "@/lib/utils";
import type { Collection, GameCollection } from "@/types";
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

export function HomeScreen({ games, customs, covers, onScan, onOpenGame, onOpenCustom }: Props) {
  const totalCards = games.reduce((s, g) => s + g.card_count, 0);
  const trackedGames = games.filter((g) => g.game !== "").length;

  const stats = [
    { value: fmt(totalCards), label: "Cards" },
    { value: fmt(trackedGames), label: "Games tracked" },
    { value: fmt(customs.length), label: "Custom collections" },
  ];

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
                <span className="text-xs n-n400 whitespace-nowrap tabular-nums">{cards(g.card_count)}</span>
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
