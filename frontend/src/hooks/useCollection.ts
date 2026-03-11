import { useEffect, useState } from "react";
import {
  DeleteCard,
  ExportCollectionCSV,
  UpdateCard,
  GetCardImageByModel,
  GetCollectionCards,
  GetCollections,
  GetCurrentCollectionID,
  ListExportPresets,
  NewCollection,
  RenameCollection,
  SetCollection,
} from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import type { Collection, CollectionCard, ExportConfig, ExportPreset } from "@/types";

export function useCollection() {
  const [collections, setCollections] = useState<Collection[]>([]);
  const [currentCollectionId, setCurrentCollectionId] = useState<number>(0);
  const [renaming, setRenaming] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailCards, setDetailCards] = useState<CollectionCard[]>([]);
  const [detailImages, setDetailImages] = useState<Record<string, string>>({});
  const [exportOpen, setExportOpen] = useState(false);
  const [exportPresets, setExportPresets] = useState<ExportPreset[]>([]);

  useEffect(() => {
    Promise.all([GetCollections(), GetCurrentCollectionID(), ListExportPresets()])
      .then(([list, id, presets]) => {
        setCollections(list ?? []);
        setCurrentCollectionId(id);
        setExportPresets((presets ?? []) as ExportPreset[]);
      })
      .catch(() => {});
  }, []);

  async function refreshCollections() {
    const updated = await GetCollections();
    if (updated) setCollections(updated);
  }

  async function handleNewCollection() {
    try {
      const c = await NewCollection();
      setCollections((prev) => [c, ...prev]);
      setCurrentCollectionId(c.id);
    } catch {}
  }

  function handleSetCollection(idStr: string) {
    const id = parseInt(idStr, 10);
    if (isNaN(id)) return;
    SetCollection(id);
    setCurrentCollectionId(id);
  }

  async function handleRenameSubmit() {
    try {
      await RenameCollection(currentCollectionId, renameValue.trim());
      await refreshCollections();
      setRenaming(false);
    } catch (e) {
      LogDebug(`[Rename] Error: ${e}`);
    }
  }

  async function openCollectionDetail() {
    try {
      const cards = await GetCollectionCards(currentCollectionId);
      setDetailCards(cards ?? []);
      setDetailImages({});
      setDetailOpen(true);
      const imgs: Record<string, string> = {};
      const seen = new Map<string, string>();
      for (const c of cards ?? []) {
        if (!seen.has(c.card_id)) seen.set(c.card_id, c.model_name);
      }
      await Promise.all(
        [...seen.entries()].map(([cardId, modelName]) =>
          GetCardImageByModel(cardId, modelName)
            .then((img: string) => { imgs[cardId] = img; })
            .catch(() => {})
        )
      );
      setDetailImages({ ...imgs });
    } catch {}
  }

  async function handleDeleteCard(id: number) {
    try {
      await DeleteCard(id);
      setDetailCards((prev) => prev.filter((c) => c.id !== id));
      setCollections((prev) =>
        prev.map((c) =>
          c.id === currentCollectionId ? { ...c, card_count: c.card_count - 1 } : c
        )
      );
    } catch (e) {
      LogDebug(`[DeleteCard] Error: ${e}`);
    }
  }

  async function handleUpdateCard(updated: CollectionCard) {
    try {
      await UpdateCard(updated);
      setDetailCards((prev) => prev.map((c) => (c.id === updated.id ? updated : c)));
    } catch (e) {
      LogDebug(`[UpdateCard] Error: ${e}`);
    }
  }

  function handleExport() {
    if (currentCollectionId) setExportOpen(true);
  }

  async function handleExportConfirm(config: ExportConfig) {
    try {
      await ExportCollectionCSV(currentCollectionId, config);
    } catch (e) {
      LogDebug(`[Export] Error: ${e}`);
    }
    setExportOpen(false);
  }

  return {
    collections,
    currentCollectionId,
    renaming,
    setRenaming,
    renameValue,
    setRenameValue,
    handleNewCollection,
    handleSetCollection,
    handleRenameSubmit,
    refreshCollections,
    openCollectionDetail,
    handleDeleteCard,
    handleUpdateCard,
    handleExport,
    handleExportConfirm,
    exportOpen,
    setExportOpen,
    exportPresets,
    detailOpen,
    setDetailOpen,
    detailCards,
    detailImages,
  };
}
