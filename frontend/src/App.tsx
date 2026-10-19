import { useState } from "react";
import { useModel } from "@/hooks/useModel";
import { useSync } from "@/hooks/useSync";
import { defaultFilter, useCollection } from "@/hooks/useCollection";
import { Sidebar, type Screen } from "@/components/Sidebar";
import { HomeScreen } from "@/components/HomeScreen";
import { ScanScreen } from "@/components/ScanScreen";
import { CollectionScreen } from "@/components/CollectionScreen";
import { SettingsScreen } from "@/components/SettingsScreen";
import { ExportModal } from "@/components/ExportModal";
import type { CardFilter } from "@/types";

function App() {
  const [screen, setScreen] = useState<Screen>("home");
  // Last collection view opened; null until one is, then the rail's
  // Collections entry reopens it.
  const [collectionTarget, setCollectionTarget] = useState<CardFilter | null>(null);
  const model = useModel();
  const collection = useCollection();
  const sync = useSync();
  const target = collectionTarget ?? defaultFilter(collection.games);

  function openGame(game: string) {
    setCollectionTarget(game === "" ? { other_bucket: true } : { game });
    setScreen("collection");
  }

  function openCustom(id: number) {
    setCollectionTarget({ collection_id: id });
    setScreen("collection");
  }

  return (
    <div className="flex h-screen bg-background text-foreground overflow-hidden">
      <Sidebar
        screen={screen}
        onNavigate={setScreen}
        onExport={() => collection.openExport(screen === "collection" ? target : undefined)}
      />

      <main className="flex-1 min-w-0 overflow-hidden">
        {screen === "home" && (
          <HomeScreen
            games={collection.games}
            customs={collection.customs}
            covers={collection.gameCovers}
            values={collection.gameValues}
            prices={collection.prices}
            onRefreshPrices={collection.refreshPrices}
            onTogglePrices={collection.setPricesEnabled}
            onScan={() => setScreen("scan")}
            onOpenGame={openGame}
            onOpenCustom={openCustom}
          />
        )}
        {screen === "scan" && <ScanScreen model={model} collection={collection} />}
        {screen === "collection" && (
          <CollectionScreen
            collection={collection}
            target={target}
            onChangeTarget={setCollectionTarget}
            onScan={() => setScreen("scan")}
          />
        )}
        {screen === "settings" && <SettingsScreen sync={sync} />}
      </main>

      <ExportModal
        target={collection.exportTarget}
        onChangeTarget={collection.setExportTarget}
        onClose={collection.closeExport}
        games={collection.games}
        customs={collection.customs}
        presets={collection.exportPresets}
        onExport={collection.confirmExport}
      />
    </div>
  );
}

export default App;
