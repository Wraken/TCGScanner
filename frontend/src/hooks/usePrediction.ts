import { useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { GetCardImage, Predict } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import type { Prediction } from "@/types";

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
  }

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
        const canvas = canvasRef.current!;
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

  return {
    prediction,
    predictionError,
    cardImage,
    editedCardID,
    foil,
    setFoil,
    locked,
    resetCard,
    updateEditedCardID,
  };
}
