import { useState } from "react";
import { useModel } from "@/hooks/useModel";
import { useCollection } from "@/hooks/useCollection";
import { Sidebar, type Screen } from "@/components/Sidebar";
import { HomeScreen } from "@/components/HomeScreen";
import { ScanScreen } from "@/components/ScanScreen";
import { CollectionScreen } from "@/components/CollectionScreen";
import { ExportModal } from "@/components/ExportModal";
import type { CardFilter } from "@/types";

function App() {
  const [screen, setScreen] = useState<Screen>("home");
  const [collectionTarget, setCollectionTarget] = useState<CardFilter>({ game: "pokemon" });
  const model = useModel();
  const collection = useCollection();

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
      <Sidebar screen={screen} onNavigate={setScreen} />

      <main className="flex-1 min-w-0 overflow-hidden">
        {screen === "home" && (
          <HomeScreen
            games={collection.games}
            customs={collection.customs}
            onScan={() => setScreen("scan")}
            onOpenGame={openGame}
            onOpenCustom={openCustom}
          />
        )}
        {screen === "scan" && <ScanScreen model={model} collection={collection} />}
        {screen === "collection" && (
          <CollectionScreen
            collection={collection}
            target={collectionTarget}
            onChangeTarget={setCollectionTarget}
            onScan={() => setScreen("scan")}
          />
        )}
      </main>

      <ExportModal
        open={collection.exportTarget !== null}
        onOpenChange={(o) => !o && collection.closeExport()}
        presets={collection.exportPresets}
        onExport={collection.confirmExport}
      />
    </div>
  );
}

export default App;
