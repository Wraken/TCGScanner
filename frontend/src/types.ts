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

export interface Prediction {
  card_id: string;
  confidence: number;
  margin?: number;
  confident: boolean;
  detect_score?: number;
  // Detected card bounding box, normalized to [0,1] against the submitted
  // frame. Set only by the embedding pipeline.
  box?: BoxNorm;
}

export interface Collection {
  id: number;
  name: string;
  started_at: string;
  card_count: number;
}

export interface CollectionCard {
  id: number;
  card_id: string;
  foil: boolean;
  model_name: string;
  scanned_at: string;
}

export type ExportField = "card_id" | "normal_count" | "foil_count" | "total_count";

export interface ExportPreset {
  name: string;
  fields: ExportField[];
  card_id_trim_end: number;
}

export interface ExportConfig {
  fields: ExportField[];
  card_id_trim_end: number;
}
