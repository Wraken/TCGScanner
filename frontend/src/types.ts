export interface CameraDevice {
  deviceId: string;
  label: string;
}

export interface Prediction {
  card_id: string;
  confidence: number;
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
