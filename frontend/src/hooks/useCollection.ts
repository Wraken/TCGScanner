import { useEffect, useState } from "react";
import {
  DeleteCard,
  ExportCollectionCSV,
  GetCardImage,
  GetCollectionCards,
  GetCollections,
  GetCurrentCollectionID,
  NewCollection,
  RenameCollection,
  SetCollection,
} from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import type { Collection, CollectionCard } from "@/types";

export function useCollection() {
  const [collections, setCollections] = useState<Collection[]>([]);
  const [currentCollectionId, setCurrentCollectionId] = useState<number>(0);
  const [renaming, setRenaming] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailCards, setDetailCards] = useState<CollectionCard[]>([]);
  const [detailImages, setDetailImages] = useState<Record<string, string>>({});

  useEffect(() => {
    Promise.all([GetCollections(), GetCurrentCollectionID()])
      .then(([list, id]) => {
        setCollections(list ?? []);
        setCurrentCollectionId(id);
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
      const uniqueIds = [...new Set((cards ?? []).map((c) => c.card_id))];
      await Promise.all(
        uniqueIds.map((cardId) =>
          GetCardImage(cardId)
            .then((img) => { imgs[cardId] = img; })
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
      await refreshCollections();
    } catch (e) {
      LogDebug(`[DeleteCard] Error: ${e}`);
    }
  }

  function handleExport() {
    if (currentCollectionId) ExportCollectionCSV(currentCollectionId);
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
    handleExport,
    detailOpen,
    setDetailOpen,
    detailCards,
    detailImages,
  };
}
