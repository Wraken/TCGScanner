import { useEffect, useRef, useState } from "react";
import {
  CreditCard,
  CameraOff,
  RefreshCw,
  ImageIcon,
  Check,
  Loader2,
  Plus,
  Download,
  Pencil,
  X,
  List,
  Trash2,
} from "lucide-react";
import { ListModels, LoadModel, Predict, GetCardImage, SaveCard, GetCollections, GetCollectionCards, DeleteCard, ExportCollectionCSV, GetCurrentCollectionID, NewCollection, SetCollection, RenameCollection } from "../wailsjs/go/main/App";
import { LogDebug, LogInfo } from "../wailsjs/runtime/runtime";
import { stopStream } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const CAMERA_ERROR =
  "Error while accessing the camera. Please check permissions and try again.";

interface CameraDevice {
  deviceId: string;
  label: string;
}

interface Prediction {
  card_id: string;
  confidence: number;
}

interface Collection {
  id: number;
  name: string;
  started_at: string;
  card_count: number;
}

interface CollectionCard {
  id: number;
  card_id: string;
  foil: boolean;
  scanned_at: string;
}

function formatCollectionLabel(c: Collection) {
  return c.name.trim()
    ? `${c.name} (${c.card_count})`
    : `#${c.id} · ${formatDate(c.started_at)} (${c.card_count})`;
}

function formatDate(s: string) {
  // SQLite returns "YYYY-MM-DD HH:MM:SS"; if already ISO (has "T"), don't modify
  const iso = s.includes("T") ? s : s.replace(" ", "T") + "Z";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return s;
  return (
    d.toLocaleDateString("en-US", { month: "short", day: "numeric" }) +
    " · " +
    d.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false })
  );
}

function App() {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(document.createElement("canvas"));
  const predictingRef = useRef(false);
  const cameraInitRef = useRef(false);
  const cardImageTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [cameras, setCameras] = useState<CameraDevice[]>([]);
  const [selectedCamera, setSelectedCamera] = useState<string>("");
  const [cameraError, setCameraError] = useState<string>("");

  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>("");
  const [modelLoading, setModelLoading] = useState(false);
  const [modelError, setModelError] = useState<string>("");
  const [modelLoaded, setModelLoaded] = useState(false);

  const [prediction, setPrediction] = useState<Prediction | null>(null);
  const [predictionError, setPredictionError] = useState<string>("");
  const [cardImage, setCardImage] = useState<string | null>(null);
  const [editedCardID, setEditedCardID] = useState<string>("");
  const [foil, setFoil] = useState(false);

  const [collections, setCollections] = useState<Collection[]>([]);
  const [currentCollectionId, setCurrentCollectionId] = useState<number>(0);
  const [renaming, setRenaming] = useState(false);
  const [renameValue, setRenameValue] = useState("");

  const [detailOpen, setDetailOpen] = useState(false);
  const [detailCards, setDetailCards] = useState<CollectionCard[]>([]);
  const [detailImages, setDetailImages] = useState<Record<string, string>>({});

  // Derived — no separate boolean state needed
  const locked = editedCardID.trim() !== "";

  // ── Helpers ───────────────────────────────────────────────────────────

  function resetCard() {
    if (cardImageTimerRef.current !== null) {
      clearTimeout(cardImageTimerRef.current);
      cardImageTimerRef.current = null;
    }
    setPrediction(null);
    setPredictionError("");
    setCardImage(null);
    setEditedCardID("");
    setFoil(false);
  }

  async function refreshCollections() {
    const updated = await GetCollections();
    if (updated) setCollections(updated);
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
      // Load images in background
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

  async function handleDeleteCard(cardId: number) {
    try {
      await DeleteCard(cardId);
      setDetailCards((prev) => prev.filter((c) => c.id !== cardId));
      await refreshCollections();
    } catch (e) {
      LogDebug(`[DeleteCard] Error: ${e}`);
    }
  }

  // ── Camera enumeration ────────────────────────────────────────────────
  async function initCameras() {
    if (cameraInitRef.current) return;
    cameraInitRef.current = true;
    setCameraError("");
    try {
      LogInfo("[Camera] Requesting permission...");
      if (!navigator.mediaDevices) {
        LogInfo("[Camera] navigator.mediaDevices is undefined (insecure context?)");
        setCameraError(CAMERA_ERROR);
        return;
      }
      const permStream = await navigator.mediaDevices.getUserMedia({
        video: true,
      });
      stopStream(permStream);

      const devices = await navigator.mediaDevices.enumerateDevices();
      const videoDevices = devices
        .filter((d) => d.kind === "videoinput")
        .map((d, i) => ({
          deviceId: d.deviceId,
          label: d.label || `Camera ${i + 1}`,
        }));

      LogInfo(`[Camera] Found ${videoDevices.length} device(s): ${videoDevices.map(d => d.label).join(", ")}`);
      setCameras(videoDevices);
      if (videoDevices.length > 0) setSelectedCamera(videoDevices[0].deviceId);
      else setCameraError("No camera detected.");
    } catch (e) {
      LogInfo(`[Camera] Error: ${e}`);
      setCameraError(CAMERA_ERROR);
    } finally {
      cameraInitRef.current = false;
    }
  }

  useEffect(() => {
    initCameras();
  }, []);

  // ── Model list ────────────────────────────────────────────────────────
  useEffect(() => {
    ListModels()
      .then((list) => setModels(list ?? []))
      .catch(() => setModelError("Failed to list models."));
  }, []);

  // ── Collections ────────────────────────────────────────────────────────
  useEffect(() => {
    Promise.all([GetCollections(), GetCurrentCollectionID()])
      .then(([list, id]) => {
        setCollections(list ?? []);
        setCurrentCollectionId(id);
      })
      .catch(() => {});
  }, []);

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

  async function handleModelChange(name: string) {
    setSelectedModel(name);
    setModelLoaded(false);
    setModelError("");
    setModelLoading(true);
    try {
      await LoadModel(name);
      setModelLoaded(true);
    } catch (e) {
      setModelError(`Failed to load model: ${e}`);
    } finally {
      setModelLoading(false);
    }
  }

  // ── Frame capture & prediction ────────────────────────────────────────
  useEffect(() => {
    LogDebug(`[Predict] Effect: modelLoaded=${modelLoaded}, selectedCamera=${selectedCamera}, locked=${locked}`);
    if (!modelLoaded || !selectedCamera || locked) return;

    LogDebug("[Predict] Starting interval");
    const id = setInterval(async () => {
      const video = videoRef.current;
      if (!video) {
        LogDebug("[Predict] No video element");
        return;
      }
      if (video.readyState < video.HAVE_CURRENT_DATA) {
        LogDebug(`[Predict] Video not ready (readyState=${video.readyState})`);
        return;
      }
      if (predictingRef.current) return;

      predictingRef.current = true;
      try {
        const canvas = canvasRef.current;
        canvas.width = video.videoWidth;
        canvas.height = video.videoHeight;
        canvas.getContext("2d")!.drawImage(video, 0, 0);
        const base64 = canvas.toDataURL("image/jpeg", 0.8).split(",")[1];
        LogDebug(`[Predict] Sending frame (${canvas.width}x${canvas.height})`);
        const result = await Predict(base64);
        if (result) {
          const pred = result as Prediction;
          LogDebug(`[Predict] card_id=${pred.card_id}  confidence=${(pred.confidence * 100).toFixed(2)}%`);
          setPrediction(pred);
          setPredictionError("");
          if (pred.confidence >= 0.99) {
            const img = await GetCardImage(pred.card_id).catch(() => null);
            setCardImage(img);
            setEditedCardID(pred.card_id);
          }
        }
      } catch (e) {
        LogDebug(`[Predict] Error: ${e}`);
        setPredictionError(String(e));
      } finally {
        predictingRef.current = false;
      }
    }, 1000);

    return () => clearInterval(id);
  }, [modelLoaded, selectedCamera, locked]);

  // ── Camera stream ─────────────────────────────────────────────────────
  useEffect(() => {
    if (!selectedCamera) return;
    let currentStream: MediaStream | null = null;

    async function startCamera() {
      try {
        stopStream(videoRef.current?.srcObject as MediaStream | null);
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { deviceId: { exact: selectedCamera } },
        });
        currentStream = stream;
        if (videoRef.current) videoRef.current.srcObject = stream;
      } catch {
        setCameraError(CAMERA_ERROR);
      }
    }
    startCamera();

    return () => {
      stopStream(currentStream);
    };
  }, [selectedCamera]);

  return (
    <div className="flex flex-col h-screen bg-background">
      {/* ── Header ── */}
      <header className="flex items-center gap-3 px-6 py-4 border-b border-border">
        <div className="flex items-center justify-center w-9 h-9 rounded-lg bg-primary/20">
          <CreditCard className="w-5 h-5 text-primary" />
        </div>
        <div>
          <h1 className="text-base font-bold leading-none text-foreground">
            Card Scanner
          </h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            Scan and manage your trading card collection with ease
          </p>
        </div>
      </header>

      {/* ── Main ── */}
      <main className="flex flex-1 gap-4 p-4 overflow-hidden">
        {/* ── Left panel – Camera ── */}
        <Card className="flex flex-col flex-[3] overflow-hidden">
          <CardContent className="flex flex-col flex-1 p-4 gap-3">
            {/* Camera select */}
            <Select value={selectedCamera} onValueChange={setSelectedCamera}>
              <SelectTrigger>
                <SelectValue placeholder="Select a camera" />
              </SelectTrigger>
              <SelectContent>
                {cameras.map((cam) => (
                  <SelectItem key={cam.deviceId} value={cam.deviceId}>
                    {cam.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>

            {/* Video / error area */}
            <div className="relative flex flex-1 items-center justify-center rounded-lg bg-muted overflow-hidden">
              {cameraError ? (
                <div className="flex flex-col items-center gap-3 text-center p-6">
                  <CameraOff className="w-12 h-12 text-muted-foreground" />
                  <p className="text-sm text-muted-foreground max-w-xs">
                    {cameraError}
                  </p>
                  <Button variant="outline" size="sm" onClick={initCameras}>
                    <RefreshCw className="w-4 h-4" />
                    Try Again
                  </Button>
                </div>
              ) : (
                <video
                  ref={videoRef}
                  autoPlay
                  playsInline
                  className="w-full h-full object-cover"
                />
              )}
            </div>
          </CardContent>
        </Card>

        {/* ── Right panel – Card info ── */}
        <Card className="flex flex-col flex-[2] overflow-hidden">
          <CardContent className="flex flex-col flex-1 p-4 gap-4 overflow-y-auto">
            {/* Collection selector */}
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Collection
              </Label>
              <div className="flex items-center gap-2">
                <Select value={String(currentCollectionId)} onValueChange={(v) => { handleSetCollection(v); setRenaming(false); }}>
                  <SelectTrigger className="flex-1 min-w-0">
                    <SelectValue placeholder="Loading..." />
                  </SelectTrigger>
                  <SelectContent>
                    {collections.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {formatCollectionLabel(c)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button variant="outline" size="icon" title="Rename collection" disabled={!currentCollectionId}
                  onClick={() => {
                    const c = collections.find(c => c.id === currentCollectionId);
                    setRenameValue(c?.name ?? "");
                    setRenaming(true);
                  }}>
                  <Pencil className="w-4 h-4" />
                </Button>
                <Button variant="outline" size="icon" onClick={handleNewCollection} title="New collection">
                  <Plus className="w-4 h-4" />
                </Button>
                <Button variant="outline" size="icon" disabled={!currentCollectionId} onClick={openCollectionDetail} title="Collection details">
                  <List className="w-4 h-4" />
                </Button>
                <Button
                  variant="outline"
                  size="icon"
                  disabled={!currentCollectionId}
                  onClick={() => ExportCollectionCSV(currentCollectionId)}
                  title="Export CSV"
                >
                  <Download className="w-4 h-4" />
                </Button>
              </div>
              {renaming && (
                <div className="flex items-center gap-2">
                  <Input
                    autoFocus
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") handleRenameSubmit();
                      else if (e.key === "Escape") setRenaming(false);
                    }}
                    placeholder="Collection name…"
                    className="flex-1 h-8 text-sm"
                  />
                  <Button size="icon" className="h-8 w-8 shrink-0" onClick={handleRenameSubmit}>
                    <Check className="w-3.5 h-3.5" />
                  </Button>
                  <Button variant="outline" size="icon" className="h-8 w-8 shrink-0" onClick={() => setRenaming(false)}>
                    <X className="w-3.5 h-3.5" />
                  </Button>
                </div>
              )}
            </div>

            <div className="h-px bg-border" />

            {/* Model selector */}
            <div className="flex flex-col gap-1.5">
              <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Model
              </Label>
              <div className="flex items-center gap-2">
                <Select
                  value={selectedModel}
                  onValueChange={handleModelChange}
                  disabled={modelLoading}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Select a model" />
                  </SelectTrigger>
                  <SelectContent>
                    {models.map((m) => (
                      <SelectItem key={m} value={m}>{m}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {modelLoading && <Loader2 className="w-4 h-4 shrink-0 animate-spin text-muted-foreground" />}
                {modelLoaded && !modelLoading && <Check className="w-4 h-4 shrink-0 text-emerald-400" />}
              </div>
              {modelError && <p className="text-xs text-destructive">{modelError}</p>}
            </div>

            <div className="h-px bg-border" />

            {/* Detected badge */}
            {locked && (
              <Badge variant="success" className="self-start">
                <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                CARD DETECTED
              </Badge>
            )}

            {/* Card image */}
            <div className="flex items-center justify-center rounded-lg bg-muted aspect-[3/4] w-32 mx-auto overflow-hidden">
              {cardImage
                ? <img src={cardImage} alt={prediction?.card_id} className="w-full h-full object-contain" />
                : <ImageIcon className="w-10 h-10 text-muted-foreground" />
              }
            </div>

            {/* Card identity */}
            <div className="flex flex-col gap-1.5">
              {locked ? (
                <>
                  <Label className="text-xs">Card ID</Label>
                  <Input
                    value={editedCardID}
                    onChange={(e) => {
                      const id = e.target.value;
                      setEditedCardID(id);
                      setCardImage(null);
                      if (cardImageTimerRef.current !== null) clearTimeout(cardImageTimerRef.current);
                      cardImageTimerRef.current = setTimeout(async () => {
                        cardImageTimerRef.current = null;
                        const img = await GetCardImage(id).catch(() => null);
                        setCardImage(img);
                      }, 300);
                    }}
                    placeholder="e.g. ogn-001-298"
                    className="text-center font-mono text-sm"
                  />
                  {prediction && editedCardID !== prediction.card_id && (
                    <p className="text-xs text-amber-400 text-center">Overridden</p>
                  )}
                  {prediction && (
                    <p className="text-xs text-muted-foreground text-center">
                      {(prediction.confidence * 100).toFixed(1)}% confidence on {prediction.card_id}
                    </p>
                  )}
                </>
              ) : (
                <p className="text-center text-muted-foreground text-sm">—</p>
              )}
              {predictionError && (
                <p className="text-xs text-destructive text-center">{predictionError}</p>
              )}
            </div>

            <div className="h-px bg-border" />

            {/* Form */}
            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
              Card info
            </p>

            <div className="flex flex-col gap-1.5">
              <Label className="text-xs">Finish</Label>
              <div className="flex rounded-md border border-border overflow-hidden">
                <button
                  onClick={() => setFoil(false)}
                  className={`flex-1 py-1.5 text-sm transition-colors ${!foil ? "bg-primary text-primary-foreground" : "bg-background text-muted-foreground hover:bg-muted"}`}
                >
                  Normal
                </button>
                <button
                  onClick={() => setFoil(true)}
                  className={`flex-1 py-1.5 text-sm transition-colors ${foil ? "bg-primary text-primary-foreground" : "bg-background text-muted-foreground hover:bg-muted"}`}
                >
                  Foil
                </button>
              </div>
            </div>

            <div className="flex gap-2 mt-auto pt-2">
              <Button
                variant="outline"
                className="flex-1"
                onClick={resetCard}
              >
                Cancel
              </Button>
              <Button
                className="flex-1"
                disabled={!editedCardID.trim()}
                onClick={async () => {
                  const cardID = editedCardID.trim();
                  try {
                    await SaveCard(cardID, foil);
                    await refreshCollections();
                  } catch (e) {
                    LogDebug(`[SaveCard] Error: ${e}`);
                  }
                  resetCard();
                }}
              >
                <Check className="w-4 h-4" />
                Validate
              </Button>
            </div>
          </CardContent>
        </Card>
      </main>

      {/* ── Collection detail modal ── */}
      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        <DialogContent className="max-h-[80vh] flex flex-col">
          <DialogHeader>
            <DialogTitle>
              Collection cards ({detailCards.length})
            </DialogTitle>
          </DialogHeader>
          <div className="flex-1 overflow-y-auto -mx-6 px-6">
            {detailCards.length === 0 ? (
              <p className="text-sm text-muted-foreground text-center py-8">No cards in this collection.</p>
            ) : (
              <div className="flex flex-col gap-2">
                {detailCards.map((c) => (
                  <div key={c.id} className="flex items-center gap-3 rounded-lg border border-border p-2">
                    <div className="w-12 h-16 rounded bg-muted flex items-center justify-center overflow-hidden shrink-0">
                      {detailImages[c.card_id]
                        ? <img src={detailImages[c.card_id]} alt={c.card_id} className="w-full h-full object-contain" />
                        : <ImageIcon className="w-5 h-5 text-muted-foreground" />
                      }
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-mono font-medium truncate">{c.card_id}</p>
                      <div className="flex items-center gap-2 mt-0.5">
                        <Badge variant={c.foil ? "default" : "secondary"} className="text-[10px] px-1.5 py-0">
                          {c.foil ? "Foil" : "Normal"}
                        </Badge>
                        <span className="text-[11px] text-muted-foreground">{formatDate(c.scanned_at)}</span>
                      </div>
                    </div>
                    <Button variant="ghost" size="icon" className="shrink-0 h-8 w-8 text-muted-foreground hover:text-destructive" onClick={() => handleDeleteCard(c.id)}>
                      <Trash2 className="w-4 h-4" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

export default App;
