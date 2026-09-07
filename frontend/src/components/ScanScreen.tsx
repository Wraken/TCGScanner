import { useEffect, useState } from "react";
import { CameraOff, Check, CircleCheck, Plus, RefreshCw } from "lucide-react";
import { SaveCard } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
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

  const [recent, setRecent] = useState<RecentScan[]>([]);
  const [lastSaved, setLastSaved] = useState<{ card_id: string; game: string } | null>(null);
  const [saveError, setSaveError] = useState<string>("");
  const [saving, setSaving] = useState(false);
  const [changingModel, setChangingModel] = useState(false);
  const [videoSize, setVideoSize] = useState<{ w: number; h: number } | null>(null);

  const scanning = model.modelLoaded && !pred.locked;

  // Auto-load a model on first entry so scanning works without a manual step.
  useEffect(() => {
    if (model.modelLoaded || model.modelLoading || model.selectedModel || model.models.length === 0) return;
    const pick = model.models.includes(PREFERRED_MODEL) ? PREFERRED_MODEL : model.models[0];
    model.handleModelChange(pick);
  }, [model.models, model.modelLoaded, model.modelLoading, model.selectedModel]);

  async function handleSave() {
    const id = pred.editedCardID.trim();
    if (!id) return;
    setLastSaved(null);
    setSaveError("");
    setSaving(true);
    const image = pred.cardImage;
    try {
      const saved = await SaveCard(id, pred.foil);
      await collection.refresh();
      setLastSaved({ card_id: saved.card_id, game: saved.game });
      setRecent((prev) => [{ card_id: id, image }, ...prev.filter((r) => r.card_id !== id)].slice(0, 3));
    } catch (e) {
      LogDebug(`[SaveCard] Error: ${e}`);
      setSaveError(String(e));
    } finally {
      setSaving(false);
    }
    pred.resetCard();
  }

  function handleCancel() {
    setLastSaved(null);
    setSaveError("");
    pred.resetCard();
  }

  async function handleNewCustom() {
    const name = window.prompt("Name of the new collection:")?.trim();
    if (!name) return;
    const c = await collection.createCustom(name);
    if (c) {
      collection.setActiveCustom(c.id);
    } else {
      setSaveError("Could not create the collection");
    }
  }

  const detected = pred.prediction != null;
  const box = pred.prediction?.box;

  return (
    <div className="flex h-full" style={{ padding: "28px", gap: "28px" }}>
      {/* ── Left: webcam feed ─────────────────────────────────────────── */}
      <div className="flex flex-col flex-1 min-w-0" style={{ gap: "18px" }}>
        {/* Camera selector + detection tag */}
        <div className="flex items-center gap-4">
          <div className="max-w-[380px] flex-1">
            <Select value={camera.selectedCamera} onValueChange={camera.setSelectedCamera}>
              <SelectTrigger>
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
            />
            {scanning ? "Detecting" : "Paused"}
          </span>
        </div>

        {/* Viewport */}
        <div className="relative flex-1 rounded-lg overflow-hidden" style={{ background: "var(--n-bg)" }}>
          {camera.cameraError ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 text-center p-6">
              <CameraOff className="w-12 h-12 n-n500" />
              <p className="text-sm n-n400 max-w-xs">{camera.cameraError}</p>
              <button className="n-btn n-btn-secondary" onClick={camera.initCameras}>
                <RefreshCw className="w-4 h-4" />
                Retry
              </button>
            </div>
          ) : (
            <>
              <video
                ref={camera.videoRef as React.RefObject<HTMLVideoElement>}
                autoPlay
                playsInline
                onLoadedMetadata={(e) =>
                  setVideoSize({ w: e.currentTarget.videoWidth, h: e.currentTarget.videoHeight })
                }
                className="absolute inset-0 w-full h-full object-cover"
              />
              {/* Aim brackets */}
              <span className="n-bracket n-bracket-tl" />
              <span className="n-bracket n-bracket-tr" />
              <span className="n-bracket n-bracket-bl" />
              <span className="n-bracket n-bracket-br" />

              {/* Real YOLO box. The SVG uses `slice`, the same transform as the
                  video's object-cover, so image-space coords land on the card
                  with no measurement of the on-screen size. */}
              {box && videoSize && (
                <svg
                  className="absolute inset-0 w-full h-full pointer-events-none"
                  viewBox={`0 0 ${videoSize.w} ${videoSize.h}`}
                  preserveAspectRatio="xMidYMid slice"
                >
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
                    y={box.y * videoSize.h - videoSize.h * 0.012}
                    fill="var(--n-accent)"
                    fontFamily="ui-monospace, monospace"
                    fontSize={videoSize.h * 0.022}
                  >
                    card · {pct(pred.prediction!.confidence)}%
                  </text>
                </svg>
              )}
            </>
          )}
        </div>

        {/* Recent scans */}
        <div className="flex items-center gap-4 min-h-[56px]">
          <span
            className="n-n500 shrink-0"
            style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
          >
            Recent scans
          </span>
          <div className="flex items-center gap-3">
            {recent.length === 0 ? (
              <span className="text-xs n-n600">—</span>
            ) : (
              recent.map((r, i) => (
                <div key={`${r.card_id}-${i}`} className="flex items-center gap-2">
                  <div
                    className="w-10 h-14 rounded-[4px] overflow-hidden shrink-0"
                    style={{ background: "var(--n-surface)" }}
                  >
                    {r.image && <img src={r.image} alt={r.card_id} className="w-full h-full object-cover" />}
                  </div>
                  <span className="font-mono n-n500" style={{ fontSize: "11px" }}>
                    {r.card_id}
                  </span>
                </div>
              ))
            )}
          </div>
        </div>
      </div>

      {/* ── Right: capture panel ──────────────────────────────────────── */}
      <div
        className="flex flex-col w-[380px] shrink-0 overflow-y-auto"
        style={{ borderLeft: "1px solid var(--n-divider)", paddingLeft: "28px", gap: "16px" }}
      >
        {/* Optional custom collection new scans are also filed under */}
        <div>
          <label className="block text-xs n-n400 mb-1.5">Also add to</label>
          <div className="flex items-center gap-2">
            <Select
              value={String(collection.activeCustomId)}
              onValueChange={(v) => collection.setActiveCustom(parseInt(v, 10) || 0)}
            >
              <SelectTrigger className="flex-1 min-w-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">no collection</SelectItem>
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
              onClick={handleNewCustom}
            >
              <Plus className="w-4 h-4" />
            </button>
          </div>
        </div>

        {/* Detected tag */}
        {detected && (
          <span className="n-tag n-tag-accent self-start">Card detected</span>
        )}

        {/* Crop / matched reference image */}
        <div
          className="flex items-center justify-center rounded-lg overflow-hidden"
          style={{ height: "290px", background: "var(--n-surface)" }}
        >
          {pred.cardImage ? (
            <img src={pred.cardImage} alt={pred.editedCardID} className="w-full h-full object-contain" />
          ) : (
            <span className="text-xs n-n600 font-mono">card crop</span>
          )}
        </div>

        {/* Card ID */}
        <div>
          <label className="block text-xs n-n400 mb-1.5">Card ID</label>
          <input
            value={pred.editedCardID}
            onChange={(e) => pred.updateEditedCardID(e.target.value)}
            placeholder="ex. OP01-025"
            className="w-full text-center font-mono text-sm rounded-md px-3 py-2 outline-none"
            style={{ background: "var(--n-surface)", border: "1px solid var(--n-divider)" }}
          />
          {pred.prediction && (
            <p className="mt-2 text-center text-xs n-n400">
              {pred.editedCardID !== pred.prediction.card_id && pred.editedCardID.trim()
                ? "ID corrected manually"
                : `${pct(pred.prediction.confidence)}% confidence`}
            </p>
          )}
          {pred.predictionError && (
            <p className="mt-2 text-center text-xs text-destructive">{pred.predictionError}</p>
          )}
        </div>

        {/* Finish + printed language (sticky, see useCollection) */}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs n-n400 mb-1.5">Finish</label>
            <div className="flex rounded-md overflow-hidden" style={{ border: "1px solid var(--n-divider)" }}>
              <button className="n-seg-opt flex-1" data-active={!pred.foil} onClick={() => pred.setFoil(false)}>
                Normal
              </button>
              <button
                className="n-seg-opt flex-1"
                data-active={pred.foil}
                onClick={() => pred.setFoil(true)}
                style={{ borderLeft: "1px solid var(--n-divider)" }}
              >
                Foil
              </button>
            </div>
          </div>
          <div>
            <label className="block text-xs n-n400 mb-1.5">Language</label>
            <Select value={collection.scanLang} onValueChange={collection.setScanLang}>
              <SelectTrigger className="w-full">
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
          <button className="n-btn n-btn-secondary flex-1" onClick={handleCancel}>
            Cancel
          </button>
          <button
            className="n-btn n-btn-primary flex-1"
            disabled={!pred.editedCardID.trim() || saving}
            onClick={handleSave}
          >
            <Check className="w-4 h-4" />
            Save
          </button>
        </div>

        {saveError ? (
          <p className="text-center text-xs text-destructive">{saveError}</p>
        ) : (
          lastSaved && (
            <p className="text-center text-xs n-n400">
              {lastSaved.card_id} added to {gameLabel(lastSaved.game)}
            </p>
          )
        )}

        {/* Model footer */}
        <div className="mt-auto pt-4 flex items-center justify-between gap-2" style={{ fontSize: "12px" }}>
          {changingModel ? (
            <div className="flex-1">
              <Select
                value={model.selectedModel}
                onValueChange={(v) => {
                  model.handleModelChange(v);
                  setChangingModel(false);
                }}
              >
                <SelectTrigger className="h-8 text-xs">
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
                <span className="truncate">
                  Model {model.modelLoading ? "…" : model.selectedModel || "none"}
                </span>
                {model.modelLoaded && <CircleCheck className="w-3.5 h-3.5 n-brand shrink-0" />}
              </span>
              <button className="n-brand shrink-0" onClick={() => setChangingModel(true)} style={{ fontSize: "12px" }}>
                change
              </button>
            </>
          )}
        </div>
        {model.modelError && <p className="text-xs text-destructive">{model.modelError}</p>}
      </div>
    </div>
  );
}
