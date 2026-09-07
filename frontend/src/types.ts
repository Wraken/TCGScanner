export interface CameraDevice {
  deviceId: string;
  label: string;
}

export interface BoxNorm {
  x: number;
  y: number;
  w: number;
  h: number;
}

// One printing of an ambiguous match (see Prediction.candidates).
export interface Candidate {
  card_id: string;
  score: number;
}

export interface Prediction {
  card_id: string;
  confidence: number;
  margin?: number;
  confident: boolean;
  detect_score?: number;
  // Detected card bounding box, normalized to [0,1] against the submitted
  // frame. Set only by the embedding pipeline.
  box?: BoxNorm;
  // Printings this frame cannot tell apart (identical artwork), best first;
  // at least two when present. The UI asks the user which one it is.
  candidates?: Candidate[];
}

export interface Collection {
  id: number;
  name: string;
  created_at: string;
  card_count: number;
}

export interface GameCollection {
  game: string;
  card_count: number;
  foil_count: number;
}

export interface CollectionCard {
  id: number;
  card_id: string;
  game: string;
  // Custom collection this copy belongs to; 0 = none.
  collection_id: number;
  foil: boolean;
  // Printed language of this copy, ISO 639-1 ("en", "fr"...). Always one of
  // ListLanguages(); declared by the user at scan time, never detected.
  lang: string;
  model_name: string;
  scanned_at: string;
}

export interface CardLanguage {
  code: string;
  label: string;
}

// Selects one view: a game, the unknown-game bucket, or a custom collection.
// Exactly one field set (the backend rejects anything else).
export interface CardFilter {
  game?: string;
  other_bucket?: boolean;
  collection_id?: number;
}

export type ExportField = "card_id" | "normal_count" | "foil_count" | "total_count" | "language";

export interface ExportPreset {
  name: string;
  fields: ExportField[];
  card_id_trim_end: number;
}

export interface ExportConfig {
  fields: ExportField[];
  card_id_trim_end: number;
}

// Bundle card metadata (models/<model>/metadata/<game>.json). Strings are ""
// and colors [] when the source did not know.
export interface CardInfo {
  id: string;
  game: string;
  name: string;
  set: string;
  set_name: string;
  number: string;
  rarity: string;
  type: string;
  colors: string[];
}
