import { useEffect, useMemo, useState } from "react";
import { Camera, ImageIcon, Pencil, Search, Trash2, Upload } from "lucide-react";
import { GetCardImageByModel, GetCards, DeleteCard, MoveCardToCustom, UpdateCard } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import { gameLabel } from "@/lib/utils";
import { CardCopiesDialog } from "@/components/CardCopiesDialog";
import type { CardFilter, CollectionCard } from "@/types";
import type { useCollection } from "@/hooks/useCollection";

interface Props {
  collection: ReturnType<typeof useCollection>;
  target: CardFilter;
  onChangeTarget: (f: CardFilter) => void;
  onScan: () => void;
}

interface CardGroup {
  card_id: string;
  normal: number;
  foil: number;
  copies: CollectionCard[];
}

type FoilFilter = "all" | "normal" | "foil";

function sameFilter(a: CardFilter, b: CardFilter): boolean {
  return a.game === b.game && !!a.other_bucket === !!b.other_bucket && a.collection_id === b.collection_id;
}

export function CollectionScreen({ collection, target, onChangeTarget, onScan }: Props) {
  const [cards, setCards] = useState<CollectionCard[]>([]);
  const [images, setImages] = useState<Record<string, string>>({});
  const [search, setSearch] = useState("");
  const [foilFilter, setFoilFilter] = useState<FoilFilter>("all");
  const [openCard, setOpenCard] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setCards([]);
    setImages({});
    setOpenCard(null);
    GetCards(target)
      .then(async (rows) => {
        if (cancelled) return;
        const list = (rows ?? []) as CollectionCard[];
        setCards(list);
        // One image per distinct card id, loaded after the grid renders.
        const seen = new Map<string, string>();
        for (const c of list) if (!seen.has(c.card_id)) seen.set(c.card_id, c.model_name);
        const imgs: Record<string, string> = {};
        await Promise.all(
          [...seen.entries()].map(([id, model]) =>
            GetCardImageByModel(id, model)
              .then((img: string) => { imgs[id] = img; })
              .catch(() => {})
          )
        );
        if (!cancelled) setImages(imgs);
      })
      .catch((e) => LogDebug(`[CollectionScreen] ${e}`));
    return () => { cancelled = true; };
  }, [target.game, target.other_bucket, target.collection_id]);

  const groups = useMemo(() => {
    const map = new Map<string, CardGroup>();
    for (const c of cards) {
      let g = map.get(c.card_id);
      if (!g) {
        g = { card_id: c.card_id, normal: 0, foil: 0, copies: [] };
        map.set(c.card_id, g);
      }
      if (c.foil) g.foil++; else g.normal++;
      g.copies.push(c);
    }
    let list = [...map.values()].sort((a, b) => a.card_id.localeCompare(b.card_id));
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter((g) => g.card_id.toLowerCase().includes(q));
    }
    if (foilFilter === "normal") list = list.filter((g) => g.normal > 0);
    if (foilFilter === "foil") list = list.filter((g) => g.foil > 0);
    return list;
  }, [cards, search, foilFilter]);

  const title =
    target.collection_id != null && target.collection_id !== 0
      ? collection.customs.find((c) => c.id === target.collection_id)?.name ?? "Collection"
      : gameLabel(target.other_bucket ? "" : target.game ?? "");

  const openGroup = openCard ? groups.find((g) => g.card_id === openCard) ?? null : null;

  async function refreshCards() {
    try {
      const rows = (await GetCards(target)) as CollectionCard[] | null;
      setCards(rows ?? []);
    } catch (e) {
      LogDebug(`[CollectionScreen] refresh: ${e}`);
    }
    await collection.refresh();
  }

  return (
    <div className="h-full overflow-y-auto flex flex-col" style={{ padding: "28px 36px", gap: "18px" }}>
      {/* View chips: games, then customs */}
      <div className="flex flex-wrap items-center gap-2">
        {collection.games.map((g) => {
          const f: CardFilter = g.game === "" ? { other_bucket: true } : { game: g.game };
          return (
            <button
              key={g.game || "__other"}
              className="n-tag"
              data-active={sameFilter(target, f)}
              style={sameFilter(target, f) ? { borderColor: "var(--n-accent)", color: "var(--n-accent)" } : undefined}
              onClick={() => onChangeTarget(f)}
            >
              {gameLabel(g.game)} · {g.card_count}
            </button>
          );
        })}
        {collection.customs.map((c) => {
          const f: CardFilter = { collection_id: c.id };
          return (
            <button
              key={`c${c.id}`}
              className="n-tag"
              data-active={sameFilter(target, f)}
              style={sameFilter(target, f) ? { borderColor: "var(--n-accent)", color: "var(--n-accent)" } : undefined}
              onClick={() => onChangeTarget(f)}
            >
              {c.name} · {c.card_count}
            </button>
          );
        })}
      </div>

      {/* Title row */}
      <div className="flex items-center gap-3">
        <h2 className="text-[28px] leading-tight">{title}</h2>
        <span className="n-tag n-tag-neutral">{cards.length} carte{cards.length > 1 ? "s" : ""}</span>
        <div className="ml-auto flex items-center gap-2">
          {target.collection_id != null && target.collection_id !== 0 && (
            <>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Renommer"
                onClick={async () => {
                  const name = window.prompt("Nouveau nom :", title)?.trim();
                  if (name) await collection.renameCustom(target.collection_id!, name);
                }}
              >
                <Pencil className="w-4 h-4" />
              </button>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Supprimer la collection (les cartes sont conservées)"
                onClick={async () => {
                  if (!window.confirm(`Supprimer « ${title} » ? Les cartes restent dans leurs jeux.`)) return;
                  await collection.deleteCustom(target.collection_id!);
                  onChangeTarget(collection.games[0]?.game ? { game: collection.games[0].game } : { other_bucket: true });
                }}
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </>
          )}
          <button className="n-btn n-btn-secondary" onClick={() => collection.openExport(target)}>
            <Upload className="w-4 h-4" />
            Exporter
          </button>
          <button className="n-btn n-btn-primary" onClick={onScan}>
            <Camera className="w-4 h-4" />
            Scanner
          </button>
        </div>
      </div>

      {/* Search + foil filter */}
      <div className="flex items-center gap-3">
        <div
          className="flex items-center gap-2 rounded-md px-3 flex-1 max-w-[340px]"
          style={{ background: "var(--n-surface)", border: "1px solid var(--n-divider)", height: "36px" }}
        >
          <Search className="w-4 h-4 n-n500 shrink-0" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Rechercher un ID…"
            className="bg-transparent outline-none text-sm flex-1 min-w-0"
          />
        </div>
        <div className="flex rounded-md overflow-hidden" style={{ border: "1px solid var(--n-divider)" }}>
          {([["all", "Toutes"], ["normal", "Normales"], ["foil", "Foil"]] as const).map(([v, label], i) => (
            <button
              key={v}
              className="n-seg-opt"
              data-active={foilFilter === v}
              style={i > 0 ? { borderLeft: "1px solid var(--n-divider)" } : undefined}
              onClick={() => setFoilFilter(v)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* Grid */}
      {groups.length === 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <p className="text-sm n-n500">Aucune carte dans cette vue.</p>
        </div>
      ) : (
        <div className="grid gap-[14px]" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
          {groups.map((g) => (
            <div key={g.card_id} className="flex flex-col gap-1.5 cursor-pointer" onClick={() => setOpenCard(g.card_id)}>
              <div
                className="relative rounded-lg overflow-hidden"
                style={{ aspectRatio: "63 / 88", background: "var(--n-surface)" }}
              >
                {images[g.card_id] ? (
                  <img src={images[g.card_id]} alt={g.card_id} className="w-full h-full object-cover" />
                ) : (
                  <div className="w-full h-full flex items-center justify-center">
                    <ImageIcon className="w-5 h-5 n-n600" />
                  </div>
                )}
                {g.foil > 0 && (
                  <span className="absolute top-2 right-2 n-tag n-tag-accent" style={{ fontSize: "10px" }}>
                    FOIL
                  </span>
                )}
              </div>
              <div className="flex items-center justify-between px-0.5">
                <span className="font-mono n-n400" style={{ fontSize: "11px" }}>{g.card_id}</span>
                <span className="n-n500" style={{ fontSize: "11px" }}>×{g.normal + g.foil}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Footer line */}
      {cards.length > 0 && (
        <p className="text-xs n-n500">
          {cards.length} carte{cards.length > 1 ? "s" : ""} · {groups.length} unique{groups.length > 1 ? "s" : ""} — triées par numéro
        </p>
      )}

      <CardCopiesDialog
        cardId={openCard}
        copies={openGroup?.copies ?? []}
        image={openCard ? images[openCard] ?? null : null}
        customs={collection.customs}
        onClose={() => setOpenCard(null)}
        onToggleFoil={async (c) => {
          try {
            await UpdateCard({ ...c, foil: !c.foil });
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] toggle foil: ${e}`);
          }
        }}
        onMove={async (rowId, customId) => {
          try {
            await MoveCardToCustom(rowId, customId);
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] move: ${e}`);
          }
        }}
        onDelete={async (rowId) => {
          try {
            await DeleteCard(rowId);
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] delete: ${e}`);
          }
        }}
      />
    </div>
  );
}
