import { useRef, useEffect } from "react";
import { ImageIcon } from "lucide-react";
import { animate, stagger } from "animejs";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Prediction } from "@/types";

interface Props {
  locked: boolean;
  cardImage: string | null;
  prediction: Prediction | null;
  predictionError: string;
  editedCardID: string;
  onEditCardID: (id: string) => void;
  scanning: boolean;
}

// Fibonacci sphere — evenly distributed points on a unit sphere
function buildSphere(n: number) {
  const golden = Math.PI * (3 - Math.sqrt(5));
  return Array.from({ length: n }, (_, i) => {
    const y = 1 - (i / (n - 1)) * 2;
    const r = Math.sqrt(1 - y * y);
    const theta = golden * i;
    return { x: r * Math.cos(theta), y, z: r * Math.sin(theta) };
  });
}

const SPHERE_PTS = buildSphere(280);

export function CardPreview({ locked, cardImage, prediction, predictionError, editedCardID, onEditCardID, scanning }: Props) {
  const imageContainerRef = useRef<HTMLDivElement>(null);
  const detailsRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const rafRef = useRef<number>(0);

  // Globe animation while scanning with no card
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !scanning || cardImage) {
      cancelAnimationFrame(rafRef.current);
      return;
    }

    canvas.width  = canvas.clientWidth  || 200;
    canvas.height = canvas.clientHeight || 300;

    const ctx    = canvas.getContext("2d")!;
    const w      = canvas.width;
    const h      = canvas.height;
    const cx     = w / 2;
    const cy     = h / 2;
    const radius = Math.min(w, h) * 0.32;
    const fov    = w * 0.9;

    const draw = (t: number) => {
      ctx.clearRect(0, 0, w, h);

      const rotY = t * 0.00028;

      const projected = SPHERE_PTS.map(({ x, y, z }) => {
        const rx = x * Math.cos(rotY) - z * Math.sin(rotY);
        const rz = x * Math.sin(rotY) + z * Math.cos(rotY);

        // Wave: sine bands traveling across azimuthal + latitude
        const azimuth = Math.atan2(rz, rx);
        const wave    = 0.5 + 0.5 * Math.sin(azimuth * 3 + y * 2 + t * 0.004);

        // Wave deforms sphere radius so points visibly ripple
        const waveR = 1 + 0.14 * (wave - 0.5);

        const scale = fov / (fov + (1 - rz) * radius * 0.6);
        const px    = cx + rx * radius * waveR * scale;
        const py    = cy + y  * radius * waveR * scale;

        const depth = (rz + 1) / 2; // 0=back, 1=front
        return { px, py, depth, wave };
      });

      projected.sort((a, b) => a.depth - b.depth);

      projected.forEach(({ px, py, depth, wave }) => {
        const alpha = (0.05 + depth * 0.6) * (0.2 + wave * 0.8);
        const size  = (0.4 + depth * 1.6) * (0.5 + wave * 0.8);
        ctx.beginPath();
        ctx.arc(px, py, Math.max(0.3, size), 0, Math.PI * 2);
        ctx.fillStyle = `rgba(139,92,246,${Math.min(1, alpha).toFixed(2)})`;
        ctx.fill();
      });

      rafRef.current = requestAnimationFrame(draw);
    };

    rafRef.current = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(rafRef.current);
  }, [scanning, cardImage]);

  // Card image scale + fade in
  useEffect(() => {
    if (!cardImage || !imageContainerRef.current) return;
    animate(imageContainerRef.current, {
      scale:   [0.93, 1],
      opacity: [0, 1],
      duration: 350,
      easing:  "easeOutBack",
    });
  }, [cardImage]);

  // Details stagger in on lock
  useEffect(() => {
    if (!locked || !detailsRef.current) return;
    animate(detailsRef.current.children, {
      opacity:    [0, 1],
      translateY: [8, 0],
      duration:   280,
      delay:      stagger(60),
      easing:     "easeOutQuad",
    });
  }, [locked]);

  return (
    <>
      {locked && (
        <Badge variant="success" className="self-start">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
          CARD DETECTED
        </Badge>
      )}

      <div ref={imageContainerRef} className="relative flex items-center justify-center rounded-lg bg-muted flex-1 overflow-hidden">
        {cardImage ? (
          <img src={cardImage} alt={prediction?.card_id} className="w-full h-full object-contain" />
        ) : scanning ? (
          <canvas ref={canvasRef} className="w-full h-full" />
        ) : (
          <ImageIcon className="w-10 h-10 text-muted-foreground" />
        )}
      </div>

      <div ref={detailsRef} className="flex flex-col gap-1.5">
        {locked ? (
          <>
            <Label className="text-xs">Card ID</Label>
            <Input
              value={editedCardID}
              onChange={(e) => onEditCardID(e.target.value)}
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
    </>
  );
}
