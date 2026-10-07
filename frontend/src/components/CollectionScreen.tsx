import { useEffect, useMemo, useState } from "react";
import { Camera, ImageIcon, Pencil, Plus, Search, Sparkle, Trash2, Upload } from "lucide-react";
import { GetCardImageByModel, GetCardInfo, GetCardPrices, GetCards, DeleteCard, MoveCardToCustom, UpdateCard } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import { gameLabel } from "@/lib/utils";
import { copyValue, describeTotal, formatEUR, sumValues } from "@/lib/prices";
import { CardImage } from "@/components/CardImage";
import { CardCopiesDialog } from "@/components/CardCopiesDialog";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { NameDialog } from "@/components/NameDialog";
import { errorText, useNotices } from "@/components/Notices";
import { defaultFilter } from "@/hooks/useCollection";
import type { CardFilter, CardInfo, CardPrice, CollectionCard } from "@/types";
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

function cards(n: number): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? "card" : "cards"}`;
}

// The one price a tile shows for a card: the foil price when every copy is
// foil, else the normal one; on the non-English shelf when every copy is in
// another language.
function tilePrice(g: CardGroup, price: CardPrice | undefined): number | null {
  const lang = g.copies.length > 0 && g.copies.every((c) => c.lang !== "en") ? g.copies[0].lang : "en";
  return copyValue({ foil: g.normal === 0, lang }, price);
}

export function CollectionScreen({ collection, target, onChangeTarget, onScan }: Props) {
  const { notify } = useNotices();
  const [cardsList, setCards] = useState<CollectionCard[]>([]);
  const [loading, setLoading] = useState(true);
  const [images, setImages] = useState<Record<string, string>>({});
  const [search, setSearch] = useState("");
  const [foilFilter, setFoilFilter] = useState<FoilFilter>("all");
  const [openCard, setOpenCard] = useState<string | null>(null);
  const [dialog, setDialog] = useState<"new" | "rename" | "delete" | null>(null);
  // Metadata of the card shown in the dialog; null = none in the bundle.
  const [infos, setInfos] = useState<Record<string, CardInfo | null>>({});
  // Cardmarket prices of the cards in view, refetched when the guides change.
  const [prices, setPrices] = useState<Record<string, CardPrice>>({});
  useEffect(() => {
    const ids = [...new Set(cardsList.map((c) => c.card_id))];
    if (ids.length === 0) {
      setPrices({});
      return;
    }
    let cancelled = false;
    GetCardPrices(ids)
      .then((p) => { if (!cancelled) setPrices((p ?? {}) as Record<string, CardPrice>); })
      .catch((e) => {
        LogDebug(`[CollectionScreen] prices: ${e}`);
        if (!cancelled) notify("error", `Couldn't load the prices: ${errorText(e)}`);
      });
    return () => { cancelled = true; };
  }, [cardsList, collection.pricesVersion]);
  const total = describeTotal(sumValues(cardsList, prices));
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
    setLoading(true);
    GetCards(target)
      .then(async (rows) => {
        if (cancelled) return;
        const list = (rows ?? []) as CollectionCard[];
        setCards(list);
        setLoading(false);
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
      .catch((e) => {
        LogDebug(`[CollectionScreen] ${e}`);
        if (cancelled) return;
        setLoading(false);
        notify("error", `Couldn't load this collection: ${errorText(e)}`);
      });
    return () => { cancelled = true; };
  }, [target.game, target.other_bucket, target.collection_id]);

  // Unfiltered per-card_id aggregation. Kept separate from `groups` so the
  // copies dialog (looked up against this) never goes empty just because
  // the active search/foil filter no longer matches the card being edited.
  const allGroups = useMemo(() => {
    const map = new Map<string, CardGroup>();
    for (const c of cardsList) {
      let g = map.get(c.card_id);
      if (!g) {
        g = { card_id: c.card_id, normal: 0, foil: 0, copies: [] };
        map.set(c.card_id, g);
      }
      if (c.foil) g.foil++; else g.normal++;
      g.copies.push(c);
    }
    return [...map.values()].sort((a, b) => a.card_id.localeCompare(b.card_id));
  }, [cardsList]);

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

  const isCustom = target.collection_id != null && target.collection_id !== 0;
  const title = isCustom
    ? collection.customs.find((c) => c.id === target.collection_id)?.name ?? "Collection"
    : gameLabel(target.other_bucket ? "" : target.game ?? "");
  const filtered = search.trim() !== "" || foilFilter !== "all";

  const openGroup = openCard ? allGroups.find((g) => g.card_id === openCard) ?? null : null;

  // Auto-close the dialog once the card it's showing has no copies left
  // (last copy deleted, or the last foil toggled while the last remaining
  // copy stops matching: allGroups only drops the entry once truly empty).
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

  // Runs one change to a copy, then reloads; a failure is shown, not logged.
  async function editCopy(what: string, op: () => Promise<unknown>) {
    try {
      await op();
    } catch (e) {
      LogDebug(`[CollectionScreen] ${what}: ${e}`);
      notify("error", `Couldn't ${what}: ${errorText(e)}`);
    }
    await refreshCards();
  }

  async function createCollection(name: string) {
    const c = await collection.createCustom(name);
    if (c) {
      onChangeTarget({ collection_id: c.id });
      setDialog(null);
    }
  }

  async function renameCollection(name: string) {
    if (await collection.renameCustom(target.collection_id!, name)) setDialog(null);
  }

  async function deleteCollection() {
    if (!(await collection.deleteCustom(target.collection_id!))) return;
    setDialog(null);
    notify("success", `Deleted “${title}”. Its cards are still in their games.`);
    onChangeTarget(defaultFilter(collection.games));
  }

  return (
    <div className="h-full overflow-y-auto flex flex-col" style={{ padding: "28px 36px", gap: "18px" }}>
      {/* View chips: games, then customs */}
      <div role="group" aria-label="Collections" className="flex flex-wrap items-center gap-2">
        {collection.games.map((g) => {
          const f: CardFilter = g.game === "" ? { other_bucket: true } : { game: g.game };
          return (
            <button
              key={g.game || "__other"}
              className="n-chip"
              aria-pressed={sameFilter(target, f)}
              onClick={() => onChangeTarget(f)}
            >
              <span className="n-chip-label">{gameLabel(g.game)}</span>
              <span className="n-chip-count">{g.card_count}</span>
            </button>
          );
        })}
        {collection.customs.map((c) => {
          const f: CardFilter = { collection_id: c.id };
          return (
            <button
              key={`c${c.id}`}
              className="n-chip"
              aria-pressed={sameFilter(target, f)}
              title={c.name}
              onClick={() => onChangeTarget(f)}
            >
              <span className="n-chip-label">{c.name}</span>
              <span className="n-chip-count">{c.card_count}</span>
            </button>
          );
        })}
        <button className="n-chip n-chip-dashed" onClick={() => setDialog("new")}>
          <Plus className="w-3 h-3" aria-hidden="true" />
          New collection
        </button>
      </div>

      {/* Title row */}
      <div className="flex items-center gap-3 min-w-0">
        <h1 className="text-[28px] leading-tight truncate min-w-0" title={title}>{title}</h1>
        <span className="n-tag n-tag-neutral tabular-nums shrink-0">{cards(cardsList.length)}</span>
        <div className="ml-auto flex items-center gap-2 shrink-0">
          {isCustom && (
            <>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Rename collection"
                aria-label="Rename collection"
                onClick={() => setDialog("rename")}
              >
                <Pencil className="w-4 h-4" aria-hidden="true" />
              </button>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Delete collection"
                aria-label="Delete collection"
                onClick={() => setDialog("delete")}
              >
                <Trash2 className="w-4 h-4" aria-hidden="true" />
              </button>
            </>
          )}
          <button className="n-btn n-btn-secondary" onClick={() => collection.openExport(target)} aria-haspopup="dialog">
            <Upload className="w-4 h-4" aria-hidden="true" />
            Export
          </button>
          <button className="n-btn n-btn-primary" onClick={onScan}>
            <Camera className="w-4 h-4" aria-hidden="true" />
            Scan
          </button>
        </div>
      </div>

      {/* Search + foil filter */}
      <div className="flex items-center gap-3">
        <div
          className="n-field n-field-wrap flex items-center gap-2 px-3 flex-1 max-w-[340px]"
          style={{ height: "36px" }}
        >
          <Search className="w-4 h-4 n-n500 shrink-0" aria-hidden="true" />
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search by card ID…"
            aria-label="Search by card ID"
            autoComplete="off"
            spellCheck={false}
            className="bg-transparent text-sm flex-1 min-w-0"
          />
        </div>
        <div
          role="group"
          aria-label="Finish"
          className="flex rounded-md overflow-hidden"
          style={{ border: "1px solid var(--n-divider)" }}
        >
          {([["all", "All"], ["normal", "Normal"], ["foil", "Foil"]] as const).map(([v, label], i) => (
            <button
              key={v}
              className="n-seg-opt"
              aria-pressed={foilFilter === v}
              style={i > 0 ? { borderLeft: "1px solid var(--n-divider)" } : undefined}
              onClick={() => setFoilFilter(v)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* Grid */}
      {loading ? (
        <div className="flex-1" aria-busy="true" />
      ) : groups.length === 0 ? (
        <div className="flex-1 flex flex-col items-center justify-center gap-3 text-center">
          {filtered ? (
            <>
              <p className="text-sm n-n400">No card in {title} matches these filters.</p>
              <button
                className="n-btn n-btn-secondary"
                onClick={() => {
                  setSearch("");
                  setFoilFilter("all");
                }}
              >
                Clear filters
              </button>
            </>
          ) : (
            <>
              <p className="text-sm n-n400">No cards in {title} yet.</p>
              <button className="n-btn n-btn-primary" onClick={onScan}>
                <Camera className="w-4 h-4" aria-hidden="true" />
                Scan cards
              </button>
            </>
          )}
        </div>
      ) : (
        <ul className="grid gap-[14px] list-none p-0 m-0" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
          {groups.map((g) => (
            <li key={g.card_id} className="min-w-0" style={{ contentVisibility: "auto", containIntrinsicSize: "auto 260px" }}>
              <button
                type="button"
                className="flex flex-col gap-1.5 w-full rounded-lg"
                style={{ textAlign: "inherit", padding: 0 }}
                aria-label={`${g.card_id}: ${g.normal} normal, ${g.foil} foil`}
                onClick={() => setOpenCard(g.card_id)}
              >
                <CardImage
                  src={images[g.card_id] ?? null}
                  alt=""
                  className="rounded-lg w-full"
                  placeholder={
                    <div className="w-full h-full flex items-center justify-center">
                      <ImageIcon className="w-5 h-5 n-n600" aria-hidden="true" />
                    </div>
                  }
                />
                <div className="flex items-center justify-between gap-2 px-0.5 w-full min-w-0">
                  <span className="font-mono n-n400 truncate min-w-0" style={{ fontSize: "11px" }} translate="no">
                    {g.card_id}
                  </span>
                  <span className="flex items-center gap-1.5 shrink-0 tabular-nums" style={{ fontSize: "11px" }} aria-hidden="true">
                    {(() => {
                      const v = tilePrice(g, prices[g.card_id]);
                      return v !== null && <span className="n-n400">{formatEUR(v)}</span>;
                    })()}
                    {g.normal > 0 && <span className="n-n400">×{g.normal}</span>}
                    {g.foil > 0 && (
                      <span className="n-brand inline-flex items-center gap-0.5">
                        <Sparkle className="w-3 h-3" aria-hidden="true" />
                        {g.foil}
                      </span>
                    )}
                  </span>
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}

      {/* Footer line */}
      {!loading && cardsList.length > 0 && (
        <p className="text-xs n-n500 tabular-nums">
          {cards(cardsList.length)}, {groups.length.toLocaleString("en-US")} unique, sorted by card ID
          {total && ` · ${total}`}
        </p>
      )}

      <CardCopiesDialog
        cardId={openCard}
        copies={openGroup?.copies ?? []}
        image={openCard ? images[openCard] ?? null : null}
        info={openCard && Object.hasOwn(infos, openCard) ? infos[openCard] : null}
        price={openCard ? prices[openCard] : undefined}
        customs={collection.customs}
        languages={collection.languages}
        onChangeLang={(c, lang) => editCopy("change the language", () => UpdateCard({ ...c, lang }))}
        onClose={() => setOpenCard(null)}
        onToggleFoil={(c) => editCopy("change the finish", () => UpdateCard({ ...c, foil: !c.foil }))}
        onMove={(rowId, customId) => editCopy("move the copy", () => MoveCardToCustom(rowId, customId))}
        onDelete={(rowId) => editCopy("delete the copy", () => DeleteCard(rowId))}
      />

      <NameDialog
        open={dialog === "new"}
        onOpenChange={(o) => !o && setDialog(null)}
        title="New custom collection"
        submitLabel="Create"
        onSubmit={createCollection}
      />
      <NameDialog
        open={dialog === "rename"}
        onOpenChange={(o) => !o && setDialog(null)}
        title="Rename collection"
        submitLabel="Rename"
        initialValue={title}
        onSubmit={renameCollection}
      />
      <ConfirmDialog
        open={dialog === "delete"}
        onOpenChange={(o) => !o && setDialog(null)}
        title={`Delete “${title}”?`}
        description="The collection goes away. Its cards stay in their games."
        confirmLabel="Delete collection"
        onConfirm={deleteCollection}
      />
    </div>
  );
}
