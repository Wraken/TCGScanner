import { useEffect, useState } from "react";
import type { CSSProperties, ReactNode } from "react";

interface Props {
  src: string | null;
  alt: string;
  // Sizing and rounding of the box (width, shrink, radius...). With
  // `adaptive`, the box carries data-orientation="portrait|landscape" so the
  // caller can size it per orientation, e.g.
  // `w-72 data-[orientation=landscape]:w-[22rem]`.
  className?: string;
  // Follow the loaded image's orientation (landscape battlefields) instead of
  // keeping a portrait box.
  adaptive?: boolean;
  // Rendered while there is no image.
  placeholder?: ReactNode;
  style?: CSSProperties;
}

const PORTRAIT = "63 / 88";
const LANDSCAPE = "88 / 63";

// Card artwork box. The image is always shown whole (object-contain) on the
// surface colour, so a landscape card in a portrait box is letterboxed rather
// than cropped; an adaptive box turns landscape once the image dimensions
// are known.
export function CardImage({ src, alt, className = "", adaptive = false, placeholder, style }: Props) {
  const [landscape, setLandscape] = useState(false);
  // A new image is assumed portrait until it reports its size.
  useEffect(() => {
    setLandscape(false);
  }, [src]);
  const orientation = adaptive && landscape ? "landscape" : "portrait";
  return (
    <div
      className={`relative overflow-hidden ${className}`}
      data-orientation={orientation}
      style={{
        aspectRatio: orientation === "landscape" ? LANDSCAPE : PORTRAIT,
        background: "var(--n-surface)",
        ...style,
      }}
    >
      {src ? (
        <img
          src={src}
          alt={alt}
          className="w-full h-full object-contain"
          onLoad={(e) => setLandscape(e.currentTarget.naturalWidth > e.currentTarget.naturalHeight)}
        />
      ) : (
        placeholder
      )}
    </div>
  );
}
