import { useEffect, useMemo, useState } from "react";
import { Camera, ImageIcon, Pencil, Plus, Search, Trash2, Upload } from "lucide-react";
import { GetCardImageByModel, GetCardInfo, GetCards, DeleteCard, MoveCardToCustom, UpdateCard } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import { gameLabel } from "@/lib/utils";
import { CardCopiesDialog } from "@/components/CardCopiesDialog";
import type { CardFilter, CardInfo, CollectionCard } from "@/types";
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
  // Metadata of the card shown in the dialog; null = none in the bundle.
  const [infos, setInfos] = useState<Record<string, CardInfo | null>>({});
  useEffect(() => {
    if (!openCard || Object.hasOwn(infos, openCard)) return;
    let cancelled = false;
    GetCardInfo(openCard)
      .then((info) => { if (!cancelled) setInfos((prev) => ({ ...prev, [openCard]: info as CardInfo })); })
      .catch(() => { if (!cancelled) setInfos((prev) => ({ ...prev, [openCard]: null })); });
    return () => { cancelled = true; };
  }, [openCard]);

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
        // One image per distinct card id, loaded after the grid renders, in
        // bounded batches so the view renders progressively instead of
        // waiting on the slowest of potentially hundreds of IPC calls.
        const seen = new Map<string, string>();
        for (const c of list) if (!seen.has(c.card_id)) seen.set(c.card_id, c.model_name);
        const entries = [...seen.entries()];
        const BATCH = 12;
        for (let i = 0; i < entries.length; i += BATCH) {
          if (cancelled) return;
          const chunk = entries.slice(i, i + BATCH);
          const imgs: Record<string, string> = {};
          await Promise.all(
            chunk.map(([id, model]) =>
              GetCardImageByModel(id, model)
                .then((img: string) => { imgs[id] = img; })
                .catch(() => {})
            )
          );
          if (cancelled) return;
          setImages((prev) => ({ ...prev, ...imgs }));
        }
      })
      .catch((e) => LogDebug(`[CollectionScreen] ${e}`));
    return () => { cancelled = true; };
  }, [target.game, target.other_bucket, target.collection_id]);

  // Unfiltered per-card_id aggregation. Kept separate from `groups` so the
  // copies dialog (looked up against this) never goes empty just because
  // the active search/foil filter no longer matches the card being edited.
  const allGroups = useMemo(() => {
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
    return [...map.values()].sort((a, b) => a.card_id.localeCompare(b.card_id));
  }, [cards]);

  const groups = useMemo(() => {
    let list = allGroups;
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter((g) => g.card_id.toLowerCase().includes(q));
    }
    if (foilFilter === "normal") list = list.filter((g) => g.normal > 0);
    if (foilFilter === "foil") list = list.filter((g) => g.foil > 0);
    return list;
  }, [allGroups, search, foilFilter]);

  const title =
    target.collection_id != null && target.collection_id !== 0
      ? collection.customs.find((c) => c.id === target.collection_id)?.name ?? "Collection"
      : gameLabel(target.other_bucket ? "" : target.game ?? "");

  const openGroup = openCard ? allGroups.find((g) => g.card_id === openCard) ?? null : null;

  // Auto-close the dialog once the card it's showing has no copies left
  // (last copy deleted, or the last foil toggled while the last remaining
  // copy stops matching — allGroups only drops the entry once truly empty).
  useEffect(() => {
    if (openCard && !allGroups.some((g) => g.card_id === openCard)) setOpenCard(null);
  }, [allGroups, openCard]);

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
        <button
          className="n-tag n-n500"
          style={{ border: "1px dashed var(--n-divider)" }}
          title="New custom collection"
          onClick={async () => {
            const name = window.prompt("Name of the new collection:")?.trim();
            if (!name) return;
            const c = await collection.createCustom(name);
            if (c) onChangeTarget({ collection_id: c.id });
          }}
        >
          <Plus className="w-3 h-3" />
          New
        </button>
      </div>

      {/* Title row */}
      <div className="flex items-center gap-3">
        <h2 className="text-[28px] leading-tight">{title}</h2>
        <span className="n-tag n-tag-neutral">{cards.length} card{cards.length !== 1 ? "s" : ""}</span>
        <div className="ml-auto flex items-center gap-2">
          {target.collection_id != null && target.collection_id !== 0 && (
            <>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Rename"
                onClick={async () => {
                  const name = window.prompt("New name:", title)?.trim();
                  if (name) await collection.renameCustom(target.collection_id!, name);
                }}
              >
                <Pencil className="w-4 h-4" />
              </button>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Delete the collection (cards are kept)"
                onClick={async () => {
                  if (!window.confirm(`Delete "${title}"? The cards stay in their games.`)) return;
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
            Export
          </button>
          <button className="n-btn n-btn-primary" onClick={onScan}>
            <Camera className="w-4 h-4" />
            Scan
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
            placeholder="Search an ID…"
            className="bg-transparent outline-none text-sm flex-1 min-w-0"
          />
        </div>
        <div className="flex rounded-md overflow-hidden" style={{ border: "1px solid var(--n-divider)" }}>
          {([["all", "All"], ["normal", "Normal"], ["foil", "Foil"]] as const).map(([v, label], i) => (
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
          <p className="text-sm n-n500">No cards in this view.</p>
        </div>
      ) : (
        <div className="grid gap-[14px]" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
          {groups.map((g) => (
            <button
              type="button"
              key={g.card_id}
              className="flex flex-col gap-1.5 w-full"
              style={{ textAlign: "inherit", padding: 0 }}
              onClick={() => setOpenCard(g.card_id)}
            >
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
              </div>
              <div className="flex items-center justify-between px-0.5">
                <span className="font-mono n-n400" style={{ fontSize: "11px" }}>{g.card_id}</span>
                <span
                  className="flex items-center gap-1.5"
                  style={{ fontSize: "11px" }}
                  title={`${g.normal} normal · ${g.foil} foil${g.foil !== 1 ? "s" : ""}`}
                >
                  {g.normal > 0 && <span className="n-n500">×{g.normal}</span>}
                  {g.foil > 0 && <span style={{ color: "var(--n-accent)" }}>✦{g.foil}</span>}
                </span>
              </div>
            </button>
          ))}
        </div>
      )}

      {/* Footer line */}
      {cards.length > 0 && (
        <p className="text-xs n-n500">
          {cards.length} card{cards.length !== 1 ? "s" : ""} · {groups.length} unique — sorted by number
        </p>
      )}

      <CardCopiesDialog
        cardId={openCard}
        copies={openGroup?.copies ?? []}
        image={openCard ? images[openCard] ?? null : null}
        info={openCard && Object.hasOwn(infos, openCard) ? infos[openCard] : null}
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
