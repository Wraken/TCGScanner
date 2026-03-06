import type { RefObject } from "react";
import { CameraOff, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { CameraDevice } from "@/types";

interface Props {
  videoRef: RefObject<HTMLVideoElement | null>;
  cameras: CameraDevice[];
  selectedCamera: string;
  onCameraChange: (deviceId: string) => void;
  cameraError: string;
  onRetry: () => void;
}

export function CameraPanel({ videoRef, cameras, selectedCamera, onCameraChange, cameraError, onRetry }: Props) {
  return (
    <Card className="flex flex-col flex-[3] overflow-hidden">
      <CardContent className="flex flex-col flex-1 p-4 gap-3">
        <Select value={selectedCamera} onValueChange={onCameraChange}>
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

        <div className="relative flex flex-1 items-center justify-center rounded-lg bg-muted overflow-hidden">
          {cameraError ? (
            <div className="flex flex-col items-center gap-3 text-center p-6">
              <CameraOff className="w-12 h-12 text-muted-foreground" />
              <p className="text-sm text-muted-foreground max-w-xs">{cameraError}</p>
              <Button variant="outline" size="sm" onClick={onRetry}>
                <RefreshCw className="w-4 h-4" />
                Try Again
              </Button>
            </div>
          ) : (
            <video ref={videoRef as React.RefObject<HTMLVideoElement>} autoPlay playsInline className="w-full h-full object-cover" />
          )}
        </div>
      </CardContent>
    </Card>
  );
}
