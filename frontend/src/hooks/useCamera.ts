import { useEffect, useRef, useState } from "react";
import { LogInfo } from "../../wailsjs/runtime/runtime";
import { stopStream } from "@/lib/utils";
import type { CameraDevice } from "@/types";

const CAMERA_ERROR =
  "Error while accessing the camera. Please check permissions and try again.";

export function useCamera() {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(document.createElement("canvas"));
  const cameraInitRef = useRef(false);

  const [cameras, setCameras] = useState<CameraDevice[]>([]);
  const [selectedCamera, setSelectedCamera] = useState<string>("");
  const [cameraError, setCameraError] = useState<string>("");

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
      const permStream = await navigator.mediaDevices.getUserMedia({ video: true });
      stopStream(permStream);

      const devices = await navigator.mediaDevices.enumerateDevices();
      const videoDevices = devices
        .filter((d) => d.kind === "videoinput")
        .map((d, i) => ({
          deviceId: d.deviceId,
          label: d.label || `Camera ${i + 1}`,
        }));

      LogInfo(`[Camera] Found ${videoDevices.length} device(s): ${videoDevices.map((d) => d.label).join(", ")}`);
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

  useEffect(() => {
    if (!selectedCamera) return;
    let aborted = false;

    async function startCamera() {
      try {
        stopStream(videoRef.current?.srcObject as MediaStream | null);
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { deviceId: { exact: selectedCamera } },
        });
        if (aborted) {
          stopStream(stream);
          return;
        }
        if (videoRef.current) videoRef.current.srcObject = stream;
      } catch {
        if (!aborted) setCameraError(CAMERA_ERROR);
      }
    }
    startCamera();

    return () => {
      aborted = true;
      stopStream(videoRef.current?.srcObject as MediaStream | null);
    };
  }, [selectedCamera]);

  return { videoRef, canvasRef, cameras, selectedCamera, setSelectedCamera, cameraError, initCameras };
}
