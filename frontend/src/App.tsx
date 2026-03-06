import { CreditCard } from "lucide-react";
import { SaveCard } from "../wailsjs/go/main/App";
import { LogDebug } from "../wailsjs/runtime/runtime";
import { Card, CardContent } from "@/components/ui/card";
import { useCamera } from "@/hooks/useCamera";
import { useModel } from "@/hooks/useModel";
import { useCollection } from "@/hooks/useCollection";
import { usePrediction } from "@/hooks/usePrediction";
import { CameraPanel } from "@/components/CameraPanel";
import { CollectionBar } from "@/components/CollectionBar";
import { ModelSelector } from "@/components/ModelSelector";
import { CardPreview } from "@/components/CardPreview";
import { CardForm } from "@/components/CardForm";
import { CollectionDetail } from "@/components/CollectionDetail";

function App() {
  const camera = useCamera();
  const model = useModel();
  const collection = useCollection();
  const pred = usePrediction(camera.videoRef, camera.canvasRef, model.modelLoaded, camera.selectedCamera);

  async function handleSaveCard() {
    const cardID = pred.editedCardID.trim();
    try {
      await SaveCard(cardID, pred.foil);
      await collection.refreshCollections();
    } catch (e) {
      LogDebug(`[SaveCard] Error: ${e}`);
    }
    pred.resetCard();
  }

  return (
    <div className="flex flex-col h-screen bg-background">
      <header className="flex items-center gap-3 px-6 py-4 border-b border-border">
        <div className="flex items-center justify-center w-9 h-9 rounded-lg bg-primary/20">
          <CreditCard className="w-5 h-5 text-primary" />
        </div>
        <div>
          <h1 className="text-base font-bold leading-none text-foreground">Card Scanner</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            Scan and manage your trading card collection with ease
          </p>
        </div>
      </header>

      <main className="flex flex-1 gap-4 p-4 overflow-hidden">
        <CameraPanel
          videoRef={camera.videoRef}
          cameras={camera.cameras}
          selectedCamera={camera.selectedCamera}
          onCameraChange={camera.setSelectedCamera}
          cameraError={camera.cameraError}
          onRetry={camera.initCameras}
        />

        <Card className="flex flex-col flex-[2] overflow-hidden">
          <CardContent className="flex flex-col flex-1 p-4 gap-4 overflow-y-auto">
            <CollectionBar
              collections={collection.collections}
              currentCollectionId={collection.currentCollectionId}
              onSetCollection={collection.handleSetCollection}
              onNewCollection={collection.handleNewCollection}
              onExport={collection.handleExport}
              onOpenDetail={collection.openCollectionDetail}
              renaming={collection.renaming}
              setRenaming={collection.setRenaming}
              renameValue={collection.renameValue}
              setRenameValue={collection.setRenameValue}
              onRenameSubmit={collection.handleRenameSubmit}
            />

            <div className="h-px bg-border" />

            <ModelSelector
              models={model.models}
              selectedModel={model.selectedModel}
              modelLoading={model.modelLoading}
              modelLoaded={model.modelLoaded}
              modelError={model.modelError}
              onModelChange={model.handleModelChange}
            />

            <div className="h-px bg-border" />

            <CardPreview
              locked={pred.locked}
              cardImage={pred.cardImage}
              prediction={pred.prediction}
              predictionError={pred.predictionError}
              editedCardID={pred.editedCardID}
              onEditCardID={pred.updateEditedCardID}
            />

            <div className="h-px bg-border" />

            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
              Card info
            </p>

            <CardForm
              foil={pred.foil}
              setFoil={pred.setFoil}
              editedCardID={pred.editedCardID}
              onSave={handleSaveCard}
              onCancel={pred.resetCard}
            />
          </CardContent>
        </Card>
      </main>

      <CollectionDetail
        open={collection.detailOpen}
        onOpenChange={collection.setDetailOpen}
        cards={collection.detailCards}
        images={collection.detailImages}
        onDeleteCard={collection.handleDeleteCard}
      />
    </div>
  );
}

export default App;
