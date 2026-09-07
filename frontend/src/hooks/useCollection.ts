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
import type { CardFilter, CardLanguage, Collection, ExportConfig, ExportPreset, GameCollection } from "@/types";

export function useCollection() {
  const [games, setGames] = useState<GameCollection[]>([]);
  const [customs, setCustoms] = useState<Collection[]>([]);
  // Custom collection new scans are also filed under; 0 = none. In-memory
  // only, on purpose: it resets to "no collection" on every app start.
  const [activeCustomId, setActiveCustomId] = useState(0);
  // Printed language stamped on new scans. Sticky and in-memory like
  // activeCustomId: the backend owns the value, this mirrors it.
  const [scanLang, setScanLangState] = useState("en");
  const [languages, setLanguages] = useState<CardLanguage[]>([]);
  const [exportTarget, setExportTarget] = useState<CardFilter | null>(null);
  const [exportPresets, setExportPresets] = useState<ExportPreset[]>([]);
  // Bundle cover art per game (images/<game>/_cover.*). Cached here — this
  // hook lives in App and never unmounts — so screen navigation never
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
    }
  }

  function setActiveCustom(id: number) {
    setActiveCustomId(id);
    SetActiveCustom(id).catch((e) => LogDebug(`[Collections] setActive error: ${e}`));
  }

  function setScanLang(code: string) {
    setScanLangState(code);
    SetScanLang(code).catch((e) => LogDebug(`[Collections] setScanLang error: ${e}`));
  }

  async function createCustom(name: string): Promise<Collection | null> {
    try {
      const c = (await NewCustomCollection(name)) as Collection;
      setCustoms((prev) => [c, ...prev]);
      return c;
    } catch (e) {
      LogDebug(`[Collections] create error: ${e}`);
      return null;
    }
  }

  async function renameCustom(id: number, name: string) {
    try {
      await RenameCollection(id, name);
      await refresh();
    } catch (e) {
      LogDebug(`[Collections] rename error: ${e}`);
    }
  }

  async function deleteCustom(id: number) {
    try {
      await DeleteCollection(id);
      setActiveCustomId((prev) => (prev === id ? 0 : prev));
      await refresh();
    } catch (e) {
      LogDebug(`[Collections] delete error: ${e}`);
    }
  }

  function openExport(f: CardFilter) {
    setExportTarget(f);
  }

  function closeExport() {
    setExportTarget(null);
  }

  async function confirmExport(config: ExportConfig) {
    if (!exportTarget) return;
    try {
      await ExportCSV(exportTarget, config);
    } catch (e) {
      LogDebug(`[Export] error: ${e}`);
    }
    setExportTarget(null);
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
    openExport,
    closeExport,
    exportPresets,
    confirmExport,
  };
}
