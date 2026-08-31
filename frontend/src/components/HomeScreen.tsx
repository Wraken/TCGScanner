import { Camera, ImageIcon } from "lucide-react";

interface Props {
  onScan: () => void;
  onOpenGame: (game: string) => void;
}

// Placeholder data — pixel-fidelity pass. These figures are not yet backed by
// the Go backend (no "game" entity, no set-size table, no pricing). They'll be
// wired to real aggregates when we build the collection/game model.
const STATS = [
  { value: "1 248", label: "Cartes" },
  { value: "87", label: "Scans cette semaine" },
  { value: "31", label: "Foils" },
  { value: "2 340 €", label: "Valeur estimée" },
];

const GAMES = [
  { name: "Pokémon", count: 512, set: "Écarlate & Violet", pct: 64, lastScan: "il y a 2 h" },
  { name: "One Piece", count: 326, set: "OP01 Romance Dawn", pct: 48, lastScan: "hier" },
  { name: "Lorcana", count: 238, set: "Premier chapitre", pct: 71, lastScan: "il y a 3 j" },
  { name: "Riftbound", count: 172, set: "Origines", pct: 36, lastScan: "il y a 1 sem" },
];

export function HomeScreen({ onScan, onOpenGame }: Props) {
  return (
    <div className="h-full overflow-y-auto" style={{ padding: "36px 44px" }}>
      <div className="flex flex-col" style={{ gap: "26px" }}>
        {/* Header */}
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-[32px] leading-tight">Ta collection</h2>
            <p className="mt-1 text-sm n-n400">4 jeux suivis — dernier scan il y a 2 h</p>
          </div>
          <button onClick={onScan} className="n-btn n-btn-primary shrink-0">
            <Camera className="w-4 h-4" />
            Scanner des cartes
          </button>
        </div>

        {/* Stats band */}
        <div className="flex flex-wrap gap-x-16 gap-y-6">
          {STATS.map((s) => (
            <div key={s.label} className="flex flex-col">
              <span className="text-[40px] font-medium leading-none">{s.value}</span>
              <span
                className="mt-2 n-n400"
                style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
              >
                {s.label}
              </span>
            </div>
          ))}
        </div>

        <hr className="n-hr" />

        {/* Game tiles */}
        <div className="grid gap-[18px]" style={{ gridTemplateColumns: "repeat(4, minmax(0, 1fr))" }}>
          {GAMES.map((g) => (
            <div key={g.name} className="n-tile" onClick={() => onOpenGame(g.name)}>
              {/* Visual (no artwork yet → fallback) */}
              <div
                className="flex flex-col items-center justify-center gap-2"
                style={{ height: "280px", background: "var(--n-bg)" }}
              >
                <ImageIcon className="w-6 h-6 n-n600" />
                <span className="text-xs n-n500">visuel {g.name}</span>
              </div>

              {/* Body */}
              <div style={{ padding: "14px 16px 16px" }} className="flex flex-col gap-2.5">
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-base font-medium">{g.name}</span>
                  <span className="text-xs n-n400">{g.count} cartes</span>
                </div>

                <div className="h-0.5 rounded-full overflow-hidden" style={{ background: "var(--n-neutral-800)" }}>
                  <div className="h-full" style={{ width: `${g.pct}%`, background: "var(--n-accent)" }} />
                </div>

                <div className="flex items-center justify-between" style={{ fontSize: "11px" }}>
                  <span className="n-n500">{g.set} — {g.pct} %</span>
                  <span className="n-n500">{g.lastScan}</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
