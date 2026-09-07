import { useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { GetCardImage, Predict } from "../../wailsjs/go/main/App";
import { LogDebug, LogInfo } from "../../wailsjs/runtime/runtime";
import type { Candidate, Prediction } from "@/types";

// How often to grab a frame and run detection. Measured 2026-08-27 with the
// JPEG q0.95 payload (~0.7 MB): a full no-card cycle (encode + Wails bridge +
// PNG->pipeline in Go) fits in ~150-250 ms, so 200 ms keeps the app scanning
// as fast as the pipeline allows. The predictingRef guard skips a tick whenever
// the previous prediction is still in flight, so this is a floor on the gap
// between scans, never a source of overlapping calls.
const SCAN_INTERVAL_MS = 200;

// Frames in a row with the same ambiguous candidate set before the choice
// dialog opens — the same caution as the confident lock, against flicker.
const CHOICE_STABLE_FRAMES = 3;

// Order-independent identity of a shown candidate set (last-pick memory).
function candidateKey(candidates: Candidate[]): string {
  return candidates.map((c) => c.card_id).sort().join("|");
}

export function usePrediction(
  videoRef: RefObject<HTMLVideoElement | null>,
  canvasRef: RefObject<HTMLCanvasElement>,
  modelLoaded: boolean,
  selectedCamera: string
) {
  const predictingRef = useRef(false);
  const cardImageTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [prediction, setPrediction] = useState<Prediction | null>(null);
  const [predictionError, setPredictionError] = useState<string>("");
  const [cardImage, setCardImage] = useState<string | null>(null);
  const [editedCardID, setEditedCardID] = useState<string>("");
  const [foil, setFoil] = useState(false);
  // Ambiguous printings waiting for the user's pick; null = no dialog.
  const [choice, setChoice] = useState<Candidate[] | null>(null);
  // Candidate ids of the previous ambiguous frame and how many consecutive
  // frames overlapped it. Twins hovering at the margin boundary flicker in
  // and out between frames, so sets are compared by overlap, not equality.
  const pendingIdsRef = useRef<Set<string>>(new Set());
  const pendingCountRef = useRef(0);
  // Ids of a dismissed set: frames overlapping them are ignored until a
  // frame shows unrelated candidates or no card.
  const suppressedIdsRef = useRef<Set<string>>(new Set());
  // Last printing picked per shown set (sorted ids); session-only, survives
  // resetCard.
  const lastPickRef = useRef(new Map<string, string>());

  const locked = editedCardID.trim() !== "";

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
    setChoice(null);
    resetPending(true);
  }

  function overlaps(candidates: Candidate[], ids: Set<string>): boolean {
    return candidates.some((c) => ids.has(c.card_id));
  }

  function resetPending(liftSuppression: boolean) {
    pendingIdsRef.current = new Set();
    pendingCountRef.current = 0;
    if (liftSuppression) suppressedIdsRef.current = new Set();
  }

  // Counts consecutive ambiguous frames whose candidates overlap the previous
  // ones; opens the choice with the latest set once stable. Unrelated
  // candidates lift any suppression.
  function trackCandidates(candidates: Candidate[]) {
    if (suppressedIdsRef.current.size > 0) {
      if (overlaps(candidates, suppressedIdsRef.current)) return;
      suppressedIdsRef.current = new Set();
    }
    pendingCountRef.current = overlaps(candidates, pendingIdsRef.current) ? pendingCountRef.current + 1 : 1;
    pendingIdsRef.current = new Set(candidates.map((c) => c.card_id));
    if (pendingCountRef.current >= CHOICE_STABLE_FRAMES) {
      pendingCountRef.current = 0;
      setChoice([...candidates].sort((a, b) => b.score - a.score));
    }
  }

  // The user's pick behaves like the confident lock. editedCardID is set in
  // the same tick as choice is cleared, so scanning never resumes in between.
  async function pickCandidate(cardId: string) {
    if (choice) lastPickRef.current.set(candidateKey(choice), cardId);
    setEditedCardID(cardId);
    setChoice(null);
    setCardImage(null);
    resetPending(true);
    const img = await GetCardImage(cardId).catch(() => null);
    setCardImage(img);
  }

  function dismissChoice() {
    if (choice) suppressedIdsRef.current = new Set(choice.map((c) => c.card_id));
    pendingIdsRef.current = new Set();
    pendingCountRef.current = 0;
    setChoice(null);
  }

  // Preselected tile: the last pick for this set when still offered, else
  // the best-scoring printing.
  const preselected = choice
    ? (() => {
        const last = lastPickRef.current.get(candidateKey(choice));
        return last && choice.some((c) => c.card_id === last) ? last : choice[0].card_id;
      })()
    : null;

  function updateEditedCardID(id: string) {
    setEditedCardID(id);
    setCardImage(null);
    if (cardImageTimerRef.current !== null) clearTimeout(cardImageTimerRef.current);
    cardImageTimerRef.current = setTimeout(async () => {
      cardImageTimerRef.current = null;
      const img = await GetCardImage(id).catch(() => null);
      setCardImage(img);
    }, 300);
  }

  useEffect(() => {
    LogDebug(`[Predict] Effect: modelLoaded=${modelLoaded}, selectedCamera=${selectedCamera}, locked=${locked}`);
    if (!modelLoaded || !selectedCamera || locked || choice) return;

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
        const canvas = canvasRef.current!;
        canvas.width = video.videoWidth;
        canvas.height = video.videoHeight;
        canvas.getContext("2d")!.drawImage(video, 0, 0);
        // JPEG q0.95, not PNG: a real (noisy) webcam frame is 4-5.5 MB as
        // PNG, and ~80% of the scan cycle went into encoding, bridging and
        // decoding that payload. Measured on the val set (TestJPEGQualityDrift):
        // q95 keeps the embedding cosine at 0.9997 (min 0.9984) with margin
        // drift within +/-0.004 - imperceptible to the lock rule - for ~7x
        // less payload. Lower qualities (q<=92) are NOT safe: margins moved by
        // up to -0.16 on some cards.
        const tEncode0 = performance.now();
        const base64 = canvas.toDataURL("image/jpeg", 0.95).split(",")[1];
        const tEncode = performance.now() - tEncode0;
        LogDebug(`[Predict] Sending frame (${canvas.width}x${canvas.height})`);
        const tBridge0 = performance.now();
        const result = await Predict(base64);
        const tBridge = performance.now() - tBridge0;
        // Timing probe: where does the scan wall-clock go? encode = canvas JPEG
        // encoding on the UI thread; bridge = Wails IPC + the whole Go pipeline.
        LogInfo(`[timing] encode=${tEncode.toFixed(0)}ms bridge+go=${tBridge.toFixed(0)}ms payload=${Math.round(base64.length / 1024)}KB`);
        if (result) {
          const pred = result as Prediction;
          LogDebug(`[Predict] card_id=${pred.card_id}  confidence=${(pred.confidence * 100).toFixed(2)}%  confident=${pred.confident}`);
          setPredictionError("");
          // The embedding pipeline reports an empty card_id when it finds no
          // card in the frame; show nothing rather than a 0% match.
          if (!pred.card_id) {
            setPrediction(null);
            resetPending(true);
          } else {
            setPrediction(pred);
            // The backend decides when a prediction is certain enough to lock;
            // each model type uses its own scale (saturated softmax for the
            // classifier, score-plus-margin for the embedding pipeline).
            if (pred.confident) {
              resetPending(true);
              const img = await GetCardImage(pred.card_id).catch(() => null);
              setCardImage(img);
              setEditedCardID(pred.card_id);
            } else if (pred.candidates && pred.candidates.length >= 2) {
              trackCandidates(pred.candidates);
            } else {
              resetPending(true);
            }
          }
        }
      } catch (e) {
        LogDebug(`[Predict] Error: ${e}`);
        setPredictionError(String(e));
      } finally {
        predictingRef.current = false;
      }
    }, SCAN_INTERVAL_MS);

    return () => clearInterval(id);
  }, [modelLoaded, selectedCamera, locked, choice]);

  return {
    prediction,
    predictionError,
    cardImage,
    editedCardID,
    foil,
    setFoil,
    locked,
    choice,
    preselected,
    pickCandidate,
    dismissChoice,
    resetCard,
    updateEditedCardID,
  };
}
