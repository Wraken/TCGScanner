import { useEffect, useId, useState } from "react";
import { CameraOff, Check, CircleCheck, Plus, RefreshCw, ScanLine } from "lucide-react";
import { SaveCard } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import { CardImage } from "@/components/CardImage";
import { NameDialog } from "@/components/NameDialog";
import { errorText } from "@/components/Notices";
import { VariantChoiceDialog } from "@/components/VariantChoiceDialog";
import { useCamera } from "@/hooks/useCamera";
import { useModel } from "@/hooks/useModel";
import { useCollection } from "@/hooks/useCollection";
import { usePrediction } from "@/hooks/usePrediction";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatCollectionLabel, gameLabel } from "@/lib/utils";

type ModelHook = ReturnType<typeof useModel>;
type CollectionHook = ReturnType<typeof useCollection>;

interface Props {
  model: ModelHook;
  collection: CollectionHook;
}

interface RecentScan {
  card_id: string;
  image: string | null;
}

function pct(v: number): string {
  return (v * 100).toFixed(1);
}

const PREFERRED_MODEL = "tcg";

export function ScanScreen({ model, collection }: Props) {
  const camera = useCamera();
  const pred = usePrediction(camera.videoRef, camera.canvasRef, model.modelLoaded, camera.selectedCamera);
  const uid = useId();

  const [recent, setRecent] = useState<RecentScan[]>([]);
  const [lastSaved, setLastSaved] = useState<{ card_id: string; game: string; lang: string } | null>(null);
  const [saveError, setSaveError] = useState<string>("");
  const [saving, setSaving] = useState(false);
  const [changingModel, setChangingModel] = useState(false);
  const [naming, setNaming] = useState(false);
  const [videoSize, setVideoSize] = useState<{ w: number; h: number } | null>(null);

  const scanning = model.modelLoaded && !pred.locked && pred.choice === null && !camera.cameraError;
  const status = camera.cameraError
    ? "Camera off"
    : model.modelLoading
      ? "Loading model…"
      : !model.modelLoaded
        ? "No model"
        : scanning
          ? "Detecting"
          : "Paused";

  // Auto-load a model on first entry so scanning works without a manual step.
  useEffect(() => {
    if (model.modelLoaded || model.modelLoading || model.selectedModel || model.models.length === 0) return;
    const pick = model.models.includes(PREFERRED_MODEL) ? PREFERRED_MODEL : model.models[0];
    model.handleModelChange(pick);
  }, [model.models, model.modelLoaded, model.modelLoading, model.selectedModel]);

  async function handleSave() {
    const id = pred.editedCardID.trim();
    if (!id || saving) return;
    setLastSaved(null);
    setSaveError("");
    setSaving(true);
    const image = pred.cardImage;
    try {
      const saved = await SaveCard(id, pred.foil);
      await collection.refresh();
      setLastSaved({ card_id: saved.card_id, game: saved.game, lang: saved.lang });
      setRecent((prev) => [{ card_id: id, image }, ...prev.filter((r) => r.card_id !== id)].slice(0, 3));
      pred.resetCard();
    } catch (e) {
      // The card stays locked so Save can simply be pressed again.
      LogDebug(`[SaveCard] Error: ${e}`);
      setSaveError(`Couldn't save ${id}: ${errorText(e)}. Try again.`);
    } finally {
      setSaving(false);
    }
  }

  function handleCancel() {
    setLastSaved(null);
    setSaveError("");
    pred.resetCard();
  }

  async function handleNewCustom(name: string) {
    const c = await collection.createCustom(name);
    if (c) {
      collection.setActiveCustom(c.id);
      setNaming(false);
    }
  }

  const detected = pred.prediction != null;
  const box = pred.prediction?.box;
  const ambiguous = (pred.prediction?.candidates?.length ?? 0) >= 2;
  // Announced once per result: the visible tag and panel change silently.
  const announcement = pred.locked
    ? `Card detected: ${pred.editedCardID}`
    : pred.choice
      ? "Several printings match. Pick the one in your hand."
      : "";

  return (
    <div className="flex h-full" style={{ padding: "28px", gap: "28px" }}>
      <h1 className="sr-only">Scan</h1>
      <p className="sr-only" role="status" aria-live="polite">{announcement}</p>

      {/* ── Left: webcam feed ─────────────────────────────────────────── */}
      <div className="flex flex-col flex-1 min-w-0" style={{ gap: "18px" }}>
        {/* Camera selector + detection status */}
        <div className="flex items-center gap-4">
          <div className="max-w-[380px] flex-1 min-w-0">
            <Select value={camera.selectedCamera} onValueChange={camera.setSelectedCamera}>
              <SelectTrigger aria-label="Camera">
                <SelectValue placeholder="Select a camera" />
              </SelectTrigger>
              <SelectContent>
                {camera.cameras.map((cam) => (
                  <SelectItem key={cam.deviceId} value={cam.deviceId}>
                    {cam.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <span className={scanning ? "n-tag n-tag-accent" : "n-tag n-tag-neutral"}>
            <span
              className="w-1.5 h-1.5 rounded-full"
              style={{ background: scanning ? "var(--n-accent)" : "var(--n-neutral-500)" }}
              aria-hidden="true"
            />
            {status}
          </span>
        </div>

        {/* Viewport: the whole camera frame is shown (contain), the same
            frame the detector sees, so a card is never detected off-screen. */}
        <div className="relative flex-1 min-h-0 rounded-lg overflow-hidden" style={{ background: "var(--n-surface)" }}>
          {camera.cameraError ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 text-center p-6">
              <CameraOff className="w-12 h-12 n-n500" aria-hidden="true" />
              <p className="text-sm n-n400 max-w-sm">{camera.cameraError}</p>
              <button className="n-btn n-btn-secondary" onClick={camera.initCameras}>
                <RefreshCw className="w-4 h-4" aria-hidden="true" />
                Try again
              </button>
            </div>
          ) : (
            <>
              <video
                ref={camera.videoRef as React.RefObject<HTMLVideoElement>}
                autoPlay
                playsInline
                muted
                aria-label="Camera feed"
                onLoadedMetadata={(e) =>
                  setVideoSize({ w: e.currentTarget.videoWidth, h: e.currentTarget.videoHeight })
                }
                className="absolute inset-0 w-full h-full object-contain"
              />
              {/* Aim brackets */}
              <span className="n-bracket n-bracket-tl" aria-hidden="true" />
              <span className="n-bracket n-bracket-tr" aria-hidden="true" />
              <span className="n-bracket n-bracket-bl" aria-hidden="true" />
              <span className="n-bracket n-bracket-br" aria-hidden="true" />

              {/* Real YOLO box. The SVG uses `meet`, the same transform as the
                  video's object-contain, so image-space coords land on the
                  card with no measurement of the on-screen size. */}
              {box && videoSize && (
                <svg
                  className="absolute inset-0 w-full h-full pointer-events-none"
                  viewBox={`0 0 ${videoSize.w} ${videoSize.h}`}
                  preserveAspectRatio="xMidYMid meet"
                  aria-hidden="true"
                >
                  <rect
                    x={box.x * videoSize.w}
                    y={box.y * videoSize.h}
                    width={box.w * videoSize.w}
                    height={box.h * videoSize.h}
                    rx={Math.min(box.w * videoSize.w, box.h * videoSize.h) * 0.04}
                    fill="none"
                    strokeWidth={5}
                    vectorEffect="non-scaling-stroke"
                    className="n-detect-halo"
                  />
                  <rect
                    x={box.x * videoSize.w}
                    y={box.y * videoSize.h}
                    width={box.w * videoSize.w}
                    height={box.h * videoSize.h}
                    rx={Math.min(box.w * videoSize.w, box.h * videoSize.h) * 0.04}
                    fill="none"
                    stroke="var(--n-accent)"
                    strokeWidth={2}
                    vectorEffect="non-scaling-stroke"
                    className="n-detect-svg"
                  />
                  <text
                    x={box.x * videoSize.w}
                    y={box.y * videoSize.h - videoSize.h * 0.016}
                    fill="var(--n-accent-soft)"
                    fontFamily="ui-monospace, monospace"
                    fontSize={videoSize.h * 0.032}
                    strokeWidth={videoSize.h * 0.01}
                    className="n-detect-label"
                  >
                    card · {pct(pred.prediction!.confidence)}%
                  </text>
                </svg>
              )}
            </>
          )}
        </div>

        {/* Recent scans */}
        <section className="flex items-center gap-4 min-h-[56px]" aria-labelledby={`${uid}-recent`}>
          <h2
            id={`${uid}-recent`}
            className="n-n500 shrink-0"
            style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
          >
            Recent scans
          </h2>
          <div className="flex items-center gap-3 min-w-0">
            {recent.length === 0 ? (
              <span className="text-xs n-n500">None yet</span>
            ) : (
              recent.map((r, i) => (
                <div key={`${r.card_id}-${i}`} className="flex items-center gap-2 min-w-0">
                  <CardImage src={r.image} alt="" className="w-10 h-14 rounded-[4px] shrink-0" />
                  <span className="font-mono n-n400 truncate" style={{ fontSize: "11px" }} translate="no">
                    {r.card_id}
                  </span>
                </div>
              ))
            )}
          </div>
        </section>
      </div>

      {/* ── Right: capture panel ──────────────────────────────────────── */}
      <div
        className="flex flex-col shrink-0 overflow-y-auto"
        style={{
          width: "clamp(320px, 28vw, 380px)",
          borderLeft: "1px solid var(--n-divider)",
          paddingLeft: "28px",
          gap: "16px",
        }}
      >
        {/* Optional custom collection new scans are also filed under */}
        <div>
          <label htmlFor={`${uid}-custom`} className="block text-xs n-n400 mb-1.5">Also add to</label>
          <div className="flex items-center gap-2">
            <Select
              value={String(collection.activeCustomId)}
              onValueChange={(v) => collection.setActiveCustom(parseInt(v, 10) || 0)}
            >
              <SelectTrigger id={`${uid}-custom`} className="flex-1 min-w-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">None</SelectItem>
                {collection.customs.map((c) => (
                  <SelectItem key={c.id} value={String(c.id)}>
                    {formatCollectionLabel(c)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <button
              className="n-btn n-btn-secondary n-btn-icon shrink-0"
              title="New custom collection"
              aria-label="New custom collection"
              onClick={() => setNaming(true)}
            >
              <Plus className="w-4 h-4" aria-hidden="true" />
            </button>
          </div>
        </div>

        {/* Detected tag */}
        {detected && (
          <span className="n-tag n-tag-accent self-start">
            {ambiguous && !pred.locked ? "Several printings match" : "Card detected"}
          </span>
        )}

        {/* Crop / matched reference image */}
        <div
          className="flex items-center justify-center rounded-lg overflow-hidden shrink-0"
          style={{ height: "clamp(150px, calc(100vh - 500px), 290px)", background: "var(--n-surface)" }}
        >
          {pred.cardImage ? (
            <img src={pred.cardImage} alt={pred.editedCardID} className="w-full h-full object-contain" />
          ) : (
            <div className="flex flex-col items-center gap-2 text-center px-8">
              <ScanLine className="w-6 h-6 n-n500" aria-hidden="true" />
              <p className="text-xs n-n400">Hold a card up to the camera. Its artwork shows here once it's named.</p>
            </div>
          )}
        </div>

        {/* Card ID */}
        <div>
          <label htmlFor={`${uid}-id`} className="block text-xs n-n400 mb-1.5">Card ID</label>
          <input
            id={`${uid}-id`}
            value={pred.editedCardID}
            onChange={(e) => pred.updateEditedCardID(e.target.value)}
            placeholder="e.g. OP01-025"
            autoComplete="off"
            spellCheck={false}
            translate="no"
            aria-describedby={`${uid}-id-hint`}
            className="n-field w-full text-center font-mono text-sm px-3 py-2"
          />
          <p id={`${uid}-id-hint`} className="mt-2 text-center text-xs n-n400 min-h-[16px]">
            {pred.prediction &&
              (pred.pickedCardID && pred.editedCardID === pred.pickedCardID
                ? "Printing chosen manually"
                : pred.editedCardID !== pred.prediction.card_id && pred.editedCardID.trim()
                  ? "ID corrected manually"
                  : ambiguous
                    ? `${pct(pred.prediction.confidence)}% · ${pred.prediction.candidates!.length} printings match`
                    : `${pct(pred.prediction.confidence)}% confidence`)}
          </p>
          {pred.predictionError && (
            <p className="mt-1 text-center text-xs n-danger" role="alert">
              Scanning hit an error: {errorText(pred.predictionError)}
            </p>
          )}
        </div>

        {/* Finish + printed language (sticky, see useCollection) */}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <span id={`${uid}-finish`} className="block text-xs n-n400 mb-1.5">Finish</span>
            <div
              role="group"
              aria-labelledby={`${uid}-finish`}
              className="flex h-10 rounded-md overflow-hidden"
              style={{ border: "1px solid var(--n-divider)" }}
            >
              <button className="n-seg-opt flex-1" aria-pressed={!pred.foil} onClick={() => pred.setFoil(false)}>
                Normal
              </button>
              <button
                className="n-seg-opt flex-1"
                aria-pressed={pred.foil}
                onClick={() => pred.setFoil(true)}
                style={{ borderLeft: "1px solid var(--n-divider)" }}
              >
                Foil
              </button>
            </div>
          </div>
          <div>
            <label htmlFor={`${uid}-lang`} className="block text-xs n-n400 mb-1.5">Language</label>
            <Select value={collection.scanLang} onValueChange={collection.setScanLang}>
              <SelectTrigger id={`${uid}-lang`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {collection.languages.map((l) => (
                  <SelectItem key={l.code} value={l.code}>{l.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {/* Actions */}
        <div className="flex gap-2 mt-2">
          <button className="n-btn n-btn-secondary flex-1" onClick={handleCancel} disabled={!detected && !pred.editedCardID}>
            Cancel
          </button>
          <button
            className="n-btn n-btn-primary flex-1"
            disabled={!pred.editedCardID.trim() || saving}
            onClick={handleSave}
          >
            <Check className="w-4 h-4" aria-hidden="true" />
            {saving ? "Saving…" : "Save"}
          </button>
        </div>

        <div className="min-h-[18px]">
          {saveError ? (
            <p className="text-center text-xs n-danger" role="alert">{saveError}</p>
          ) : (
            <p className="text-center text-xs n-n400" role="status">
              {lastSaved &&
                `Added ${lastSaved.card_id} (${lastSaved.lang.toUpperCase()}) to ${gameLabel(lastSaved.game)}`}
            </p>
          )}
        </div>

        {/* Model footer */}
        <div className="mt-auto pt-4 flex items-center justify-between gap-2" style={{ fontSize: "12px" }}>
          {changingModel ? (
            <div className="flex-1">
              <Select
                value={model.selectedModel}
                defaultOpen
                onOpenChange={(o) => !o && setChangingModel(false)}
                onValueChange={(v) => {
                  model.handleModelChange(v);
                  setChangingModel(false);
                }}
              >
                <SelectTrigger className="h-8 text-xs" aria-label="Model">
                  <SelectValue placeholder="Choose a model" />
                </SelectTrigger>
                <SelectContent>
                  {model.models.map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ) : (
            <>
              <span className="flex items-center gap-1.5 n-n500 min-w-0">
                <span className="truncate" translate="no">
                  {model.modelLoading
                    ? "Loading model…"
                    : model.selectedModel
                      ? `Model: ${model.selectedModel}`
                      : "No model loaded"}
                </span>
                {model.modelLoaded && <CircleCheck className="w-3.5 h-3.5 n-brand shrink-0" role="img" aria-label="Loaded" />}
              </span>
              <button
                className="n-btn n-btn-link shrink-0"
                onClick={() => setChangingModel(true)}
                disabled={model.models.length === 0}
              >
                Change
              </button>
            </>
          )}
        </div>
        {model.modelError && <p className="text-xs n-danger" role="alert">{model.modelError}</p>}
      </div>

      <VariantChoiceDialog
        candidates={pred.choice}
        preselected={pred.preselected}
        onPick={pred.pickCandidate}
        onDismiss={pred.dismissChoice}
      />

      <NameDialog
        open={naming}
        onOpenChange={setNaming}
        title="New custom collection"
        description="New scans are also filed under it while it's selected."
        submitLabel="Create"
        onSubmit={handleNewCustom}
      />
    </div>
  );
}
