import React, { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { ExportConfig, ExportField, ExportPreset } from "@/types";

const ALL_FIELDS: { key: ExportField; label: string }[] = [
  { key: "card_id", label: "Card ID" },
  { key: "normal_count", label: "Normal Count" },
  { key: "foil_count", label: "Foil Count" },
  { key: "total_count", label: "Total Count" },
];

const EXAMPLE = { cardID: "ogn-014-298", normal: 2, foil: 1 };

function previewValue(field: ExportField, trimEnd: number): string {
  switch (field) {
    case "card_id": {
      const id = EXAMPLE.cardID;
      return trimEnd > 0 && trimEnd < id.length ? id.slice(0, id.length - trimEnd) : id;
    }
    case "normal_count": return String(EXAMPLE.normal);
    case "foil_count":   return String(EXAMPLE.foil);
    case "total_count":  return String(EXAMPLE.normal + EXAMPLE.foil);
  }
}

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  presets: ExportPreset[];
  onExport: (config: ExportConfig) => void;
}

export function ExportModal({ open, onOpenChange, presets, onExport }: Props) {
  const [selectedPreset, setSelectedPreset] = useState<string>("");
  const [fields, setFields] = useState<ExportField[]>([]);
  const [trimEnd, setTrimEnd] = useState(0);

  useEffect(() => {
    if (open && presets.length > 0) {
      applyPreset(presets[0]);
    }
  }, [open, presets]);

  function applyPreset(preset: ExportPreset) {
    setSelectedPreset(preset.name);
    setFields([...preset.fields]);
    setTrimEnd(preset.card_id_trim_end);
  }

  function toggleField(key: ExportField) {
    setSelectedPreset("Custom");
    setFields((prev) =>
      prev.includes(key) ? prev.filter((f) => f !== key) : [...prev, key]
    );
  }

  function handleTrimEndChange(v: number) {
    setSelectedPreset("Custom");
    setTrimEnd(v);
  }

  const previewHeaders = fields.map((f) => ALL_FIELDS.find((m) => m.key === f)?.label ?? f);
  const previewRow = fields.map((f) => previewValue(f, trimEnd));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Export Collection</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          {/* Presets */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Preset
            </Label>
            <div className="flex flex-wrap gap-2">
              {presets.map((p) => (
                <Button
                  key={p.name}
                  variant={selectedPreset === p.name ? "default" : "outline"}
                  size="sm"
                  onClick={() => applyPreset(p)}
                >
                  {p.name}
                </Button>
              ))}
              <Button
                variant={selectedPreset === "Custom" ? "default" : "outline"}
                size="sm"
                onClick={() => setSelectedPreset("Custom")}
              >
                Custom
              </Button>
            </div>
          </div>

          {/* Fields */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Fields
            </Label>
            <div className="flex flex-col gap-1.5">
              {ALL_FIELDS.map(({ key, label }) => (
                <label key={key} className="flex items-center gap-2 cursor-pointer select-none text-sm">
                  <input
                    type="checkbox"
                    checked={fields.includes(key)}
                    onChange={() => toggleField(key)}
                    className="accent-primary w-4 h-4"
                  />
                  {label}
                </label>
              ))}
            </div>
          </div>

          {/* Card ID trim */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Card ID — remove last characters
              </Label>
              <span className="text-xs font-mono text-muted-foreground tabular-nums">
                {trimEnd === 0 ? "none" : `-${trimEnd}`}
              </span>
            </div>
            <input
              type="range"
              min={0}
              max={EXAMPLE.cardID.length - 1}
              value={trimEnd}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleTrimEndChange(Number(e.target.value))}
              className="w-full accent-primary"
            />
            <p className="text-xs text-muted-foreground">
              Drag to trim · e.g. 4 turns <code className="font-mono">ogn-014-298</code> → <code className="font-mono">ogn-014</code>
            </p>
          </div>

          {/* Preview */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Preview
            </Label>
            {fields.length > 0 ? (
              <div className="text-xs font-mono bg-muted rounded p-2 space-y-1">
                <div className="text-muted-foreground">{previewHeaders.join(", ")}</div>
                <div>{previewRow.join(", ")}</div>
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">No fields selected.</p>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={fields.length === 0}
            onClick={() => onExport({ fields, card_id_trim_end: trimEnd })}
          >
            Export
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
