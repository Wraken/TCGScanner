import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Collection } from "@/types";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function stopStream(stream: MediaStream | null | undefined) {
  stream?.getTracks().forEach((t) => t.stop());
}

export function formatDate(s: string): string {
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

export function formatCollectionLabel(c: Collection): string {
  return `${c.name} (${c.card_count})`;
}

const GAME_LABELS: Record<string, string> = {
  pokemon: "Pokémon",
  onepiece: "One Piece",
  lorcana: "Lorcana",
  riftbound: "Riftbound",
  "": "Other",
};

export function gameLabel(game: string): string {
  return GAME_LABELS[game] ?? game;
}
