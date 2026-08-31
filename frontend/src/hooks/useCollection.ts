import { useEffect, useState } from "react";
import {
  DeleteCollection,
  ExportCSV,
  GetCustomCollections,
  GetGameCollections,
  ListExportPresets,
  NewCustomCollection,
  RenameCollection,
  SetActiveCustom,
} from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import type { CardFilter, Collection, ExportConfig, ExportPreset, GameCollection } from "@/types";

export function useCollection() {
  const [games, setGames] = useState<GameCollection[]>([]);
  const [customs, setCustoms] = useState<Collection[]>([]);
  // Custom collection new scans are also filed under; 0 = none. In-memory
  // only, on purpose: it resets to "aucune" on every app start.
  const [activeCustomId, setActiveCustomId] = useState(0);
  const [exportTarget, setExportTarget] = useState<CardFilter | null>(null);
  const [exportPresets, setExportPresets] = useState<ExportPreset[]>([]);

  useEffect(() => {
    refresh();
    ListExportPresets()
      .then((p) => setExportPresets((p ?? []) as ExportPreset[]))
      .catch(() => {});
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
    activeCustomId,
    refresh,
    setActiveCustom,
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
