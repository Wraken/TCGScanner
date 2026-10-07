import { useEffect, useId, useState } from "react";
import { Upload } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { gameLabel } from "@/lib/utils";
import type { CardFilter, Collection, ExportConfig, ExportField, ExportPreset, GameCollection } from "@/types";

const ALL_FIELDS: { key: ExportField; label: string }[] = [
  { key: "card_id", label: "Card ID" },
  { key: "normal_count", label: "Normal count" },
  { key: "foil_count", label: "Foil count" },
  { key: "total_count", label: "Total count" },
  { key: "language", label: "Language" },
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
    case "language":     return "en";
  }
}

// A CardFilter as one Select value, and back.
function filterKey(f: CardFilter): string {
  if (f.collection_id) return `c:${f.collection_id}`;
  if (f.other_bucket) return "other";
  return `g:${f.game ?? ""}`;
}
function keyFilter(k: string): CardFilter {
  if (k.startsWith("c:")) return { collection_id: Number(k.slice(2)) };
  if (k === "other") return { other_bucket: true };
  return { game: k.slice(2) };
}

interface Props {
  target: CardFilter | null; // null = closed
  onChangeTarget: (f: CardFilter) => void;
  onClose: () => void;
  games: GameCollection[];
  customs: Collection[];
  presets: ExportPreset[];
  onExport: (target: CardFilter, config: ExportConfig) => Promise<boolean>;
}

export function ExportModal({ target, onChangeTarget, onClose, games, customs, presets, onExport }: Props) {
  const open = target !== null;
  const [selectedPreset, setSelectedPreset] = useState<string>("");
  const [fields, setFields] = useState<ExportField[]>([]);
  const [trimEnd, setTrimEnd] = useState(0);
  const [busy, setBusy] = useState(false);
  const uid = useId();

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

  async function handleExport() {
    if (!target || busy) return;
    setBusy(true);
    try {
      await onExport(target, { fields, card_id_trim_end: trimEnd });
    } finally {
      setBusy(false);
    }
  }

  const previewHeaders = fields.map((f) => ALL_FIELDS.find((m) => m.key === f)?.label ?? f);
  const previewRow = fields.map((f) => previewValue(f, trimEnd));
  const views = [
    ...games.map((g) => ({ key: g.game === "" ? "other" : `g:${g.game}`, label: `${gameLabel(g.game)} (${g.card_count})` })),
    ...customs.map((c) => ({ key: `c:${c.id}`, label: `${c.name} (${c.card_count})` })),
  ];

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Export to CSV</DialogTitle>
          <DialogDescription>Pick a collection and its columns, then choose where to save the file.</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {/* What to export */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor={`${uid}-view`} className="text-xs n-n400">Collection</label>
            <Select value={target ? filterKey(target) : ""} onValueChange={(k) => onChangeTarget(keyFilter(k))}>
              <SelectTrigger id={`${uid}-view`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {views.map((v) => (
                  <SelectItem key={v.key} value={v.key}>{v.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Presets */}
          <div className="flex flex-col gap-1.5">
            <span id={`${uid}-preset`} className="text-xs n-n400">Format</span>
            <div role="group" aria-labelledby={`${uid}-preset`} className="flex flex-wrap gap-2">
              {[...presets.map((p) => p.name), "Custom"].map((name) => (
                <button
                  key={name}
                  type="button"
                  className="n-chip"
                  aria-pressed={selectedPreset === name}
                  onClick={() => {
                    const p = presets.find((x) => x.name === name);
                    if (p) applyPreset(p);
                    else setSelectedPreset("Custom");
                  }}
                >
                  {name}
                </button>
              ))}
            </div>
          </div>

          {/* Fields */}
          <fieldset className="border-0 p-0 m-0">
            <legend className="text-xs n-n400 mb-1.5">Columns</legend>
            <div className="grid grid-cols-2 gap-x-4 gap-y-1.5">
              {ALL_FIELDS.map(({ key, label }) => (
                <label key={key} className="flex items-center gap-2.5 cursor-pointer select-none text-sm py-0.5">
                  <input
                    type="checkbox"
                    checked={fields.includes(key)}
                    onChange={() => toggleField(key)}
                    className="w-4 h-4 accent-[var(--n-accent)]"
                  />
                  {label}
                </label>
              ))}
            </div>
            {fields.includes("language") && (
              <p className="text-xs n-n500 mt-1.5">Splits each card into one row per printed language.</p>
            )}
          </fieldset>

          {/* Card ID trim */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <label htmlFor={`${uid}-trim`} className="text-xs n-n400">Shorten card IDs</label>
              <span className="text-xs font-mono n-n400 tabular-nums" aria-hidden="true">
                {trimEnd === 0 ? "off" : `−${trimEnd}`}
              </span>
            </div>
            <input
              id={`${uid}-trim`}
              type="range"
              min={0}
              max={EXAMPLE.cardID.length - 1}
              value={trimEnd}
              aria-valuetext={trimEnd === 0 ? "Off" : `Remove the last ${trimEnd} characters`}
              onChange={(e) => handleTrimEndChange(Number(e.target.value))}
              className="w-full accent-[var(--n-accent)]"
            />
            <p className="text-xs n-n500">
              Removes characters from the end. At 4, <code className="font-mono">ogn-014-298</code> becomes{" "}
              <code className="font-mono">ogn-014</code>.
            </p>
          </div>

          {/* Preview */}
          <div className="flex flex-col gap-1.5">
            <span className="text-xs n-n400">Preview</span>
            {fields.length > 0 ? (
              <div className="text-xs font-mono rounded-md p-2.5 flex flex-col gap-1 n-surface" translate="no">
                <div className="n-n400">{previewHeaders.join(", ")}</div>
                <div>{previewRow.join(", ")}</div>
              </div>
            ) : (
              <p className="text-xs n-n400">Pick at least one column.</p>
            )}
          </div>
        </div>

        <DialogFooter>
          <button type="button" className="n-btn n-btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="n-btn n-btn-primary"
            disabled={fields.length === 0 || busy}
            onClick={handleExport}
          >
            <Upload className="w-4 h-4" aria-hidden="true" />
            {busy ? "Exporting…" : "Export"}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
