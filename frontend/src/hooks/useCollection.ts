import { useEffect, useState } from "react";
import {
  DeleteCollection,
  ExportCSV,
  GetActiveCustom,
  GetCustomCollections,
  GetGameCollections,
  GetGameCover,
  GetScanLang,
  ListExportPresets,
  ListLanguages,
  NewCustomCollection,
  RenameCollection,
  SetActiveCustom,
  SetScanLang,
} from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import { errorText, useNotices } from "@/components/Notices";
import type { CardFilter, CardLanguage, Collection, ExportConfig, ExportPreset, GameCollection } from "@/types";

// The view to open when nothing else says which: the first game holding
// cards, else the first known game, else the unknown-game bucket.
export function defaultFilter(games: GameCollection[]): CardFilter {
  const g = games.find((x) => x.game !== "" && x.card_count > 0) ?? games.find((x) => x.game !== "");
  return g ? { game: g.game } : { other_bucket: true };
}

export function useCollection() {
  const { notify } = useNotices();
  const [games, setGames] = useState<GameCollection[]>([]);
  const [customs, setCustoms] = useState<Collection[]>([]);
  // Custom collection new scans are also filed under; 0 = none. In-memory
  // only, on purpose: it resets to "no collection" on every app start.
  const [activeCustomId, setActiveCustomId] = useState(0);
  // Printed language stamped on new scans. Sticky and in-memory like
  // activeCustomId: the backend owns the value, this mirrors it.
  const [scanLang, setScanLangState] = useState("en");
  const [languages, setLanguages] = useState<CardLanguage[]>([]);
  // The export dialog's view; null = dialog closed.
  const [exportTarget, setExportTarget] = useState<CardFilter | null>(null);
  const [exportPresets, setExportPresets] = useState<ExportPreset[]>([]);
  // Bundle cover art per game (images/<game>/_cover.*). Cached here (this
  // hook lives in App and never unmounts) so screen navigation never
  // refetches. Games without a cover are simply absent from the map, and get
  // retried on every refresh: dropping a _cover file while the app runs makes
  // it appear after the next scan/refresh.
  const [gameCovers, setGameCovers] = useState<Record<string, string>>({});

  useEffect(() => {
    let cancelled = false;
    for (const g of games) {
      if (!g.game || g.game in gameCovers) continue;
      GetGameCover(g.game)
        .then((img) => {
          if (!cancelled) setGameCovers((prev) => (g.game in prev ? prev : { ...prev, [g.game]: img }));
        })
        .catch(() => {});
    }
    return () => {
      cancelled = true;
    };
  }, [games]);

  useEffect(() => {
    refresh();
    ListExportPresets()
      .then((p) => setExportPresets((p ?? []) as ExportPreset[]))
      .catch(() => {});
    GetActiveCustom().then(setActiveCustomId).catch(() => {});
    ListLanguages()
      .then((l) => setLanguages((l ?? []) as CardLanguage[]))
      .catch(() => {});
    GetScanLang().then(setScanLangState).catch(() => {});
  }, []);

  async function refresh() {
    try {
      const [g, c] = await Promise.all([GetGameCollections(), GetCustomCollections()]);
      setGames((g ?? []) as GameCollection[]);
      setCustoms((c ?? []) as Collection[]);
    } catch (e) {
      LogDebug(`[Collections] refresh error: ${e}`);
      notify("error", `Couldn't load your collections: ${errorText(e)}`);
    }
  }

  function setActiveCustom(id: number) {
    const prev = activeCustomId;
    setActiveCustomId(id);
    SetActiveCustom(id).catch((e) => {
      LogDebug(`[Collections] setActive error: ${e}`);
      setActiveCustomId(prev);
      notify("error", `Couldn't change the collection new scans go to: ${errorText(e)}`);
    });
  }

  function setScanLang(code: string) {
    setScanLangState(code);
    SetScanLang(code).catch((e) => {
      LogDebug(`[Collections] setScanLang error: ${e}`);
      GetScanLang().then(setScanLangState).catch(() => {});
      notify("error", `Couldn't change the scan language: ${errorText(e)}`);
    });
  }

  async function createCustom(name: string): Promise<Collection | null> {
    try {
      const c = (await NewCustomCollection(name)) as Collection;
      setCustoms((prev) => [c, ...prev]);
      return c;
    } catch (e) {
      LogDebug(`[Collections] create error: ${e}`);
      notify("error", `Couldn't create “${name}”: ${errorText(e)}`);
      return null;
    }
  }

  async function renameCustom(id: number, name: string): Promise<boolean> {
    try {
      await RenameCollection(id, name);
      await refresh();
      return true;
    } catch (e) {
      LogDebug(`[Collections] rename error: ${e}`);
      notify("error", `Couldn't rename the collection: ${errorText(e)}`);
      return false;
    }
  }

  async function deleteCustom(id: number): Promise<boolean> {
    try {
      await DeleteCollection(id);
      setActiveCustomId((prev) => (prev === id ? 0 : prev));
      await refresh();
      return true;
    } catch (e) {
      LogDebug(`[Collections] delete error: ${e}`);
      notify("error", `Couldn't delete the collection: ${errorText(e)}`);
      return false;
    }
  }

  function openExport(f?: CardFilter) {
    setExportTarget(f ?? defaultFilter(games));
  }

  function closeExport() {
    setExportTarget(null);
  }

  // Resolves to true once the file is written. A cancelled save dialog comes
  // back as an empty path: the export dialog stays open for another try.
  async function confirmExport(target: CardFilter, config: ExportConfig): Promise<boolean> {
    try {
      const path = (await ExportCSV(target, config)) as string;
      if (!path) return false;
      const file = path.split(/[\\/]/).pop() ?? path;
      notify("success", `Exported to ${file}`);
      setExportTarget(null);
      return true;
    } catch (e) {
      LogDebug(`[Export] error: ${e}`);
      notify("error", `Export failed: ${errorText(e)}`);
      return false;
    }
  }

  return {
    games,
    customs,
    gameCovers,
    activeCustomId,
    refresh,
    setActiveCustom,
    languages,
    scanLang,
    setScanLang,
    createCustom,
    renameCustom,
    deleteCustom,
    exportTarget,
    setExportTarget,
    openExport,
    closeExport,
    exportPresets,
    confirmExport,
  };
}
