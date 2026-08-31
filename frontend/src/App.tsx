import { useState } from "react";
import { useModel } from "@/hooks/useModel";
import { useCollection } from "@/hooks/useCollection";
import { Sidebar, type Screen } from "@/components/Sidebar";
import { HomeScreen } from "@/components/HomeScreen";
import { ScanScreen } from "@/components/ScanScreen";
import { CollectionDetail } from "@/components/CollectionDetail";
import { ExportModal } from "@/components/ExportModal";

function App() {
  const [screen, setScreen] = useState<Screen>("home");
  const model = useModel();
  const collection = useCollection();

  return (
    <div className="flex h-screen bg-background text-foreground overflow-hidden">
      <Sidebar
        screen={screen}
        onNavigate={setScreen}
        onCollections={collection.openCollectionDetail}
        onExport={collection.handleExport}
      />

      <main className="flex-1 min-w-0 overflow-hidden">
        {screen === "home" ? (
          <HomeScreen onScan={() => setScreen("scan")} onOpenGame={() => setScreen("scan")} />
        ) : (
          <ScanScreen model={model} collection={collection} />
        )}
      </main>

      <CollectionDetail
        open={collection.detailOpen}
        onOpenChange={collection.setDetailOpen}
        cards={collection.detailCards}
        images={collection.detailImages}
        onUpdateCard={collection.handleUpdateCard}
        onDeleteCard={collection.handleDeleteCard}
      />

      <ExportModal
        open={collection.exportOpen}
        onOpenChange={collection.setExportOpen}
        presets={collection.exportPresets}
        onExport={collection.handleExportConfirm}
      />
    </div>
  );
}

export default App;
