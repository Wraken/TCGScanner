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
  scanned_at: string;
}
