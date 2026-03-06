import { Check, Download, List, Pencil, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatCollectionLabel } from "@/lib/utils";
import type { Collection } from "@/types";

interface Props {
  collections: Collection[];
  currentCollectionId: number;
  onSetCollection: (idStr: string) => void;
  onNewCollection: () => void;
  onExport: () => void;
  onOpenDetail: () => void;
  renaming: boolean;
  setRenaming: (v: boolean) => void;
  renameValue: string;
  setRenameValue: (v: string) => void;
  onRenameSubmit: () => void;
}

export function CollectionBar({
  collections,
  currentCollectionId,
  onSetCollection,
  onNewCollection,
  onExport,
  onOpenDetail,
  renaming,
  setRenaming,
  renameValue,
  setRenameValue,
  onRenameSubmit,
}: Props) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        Collection
      </Label>
      <div className="flex items-center gap-2">
        <Select
          value={String(currentCollectionId)}
          onValueChange={(v) => { onSetCollection(v); setRenaming(false); }}
        >
          <SelectTrigger className="flex-1 min-w-0">
            <SelectValue placeholder="Loading..." />
          </SelectTrigger>
          <SelectContent>
            {collections.map((c) => (
              <SelectItem key={c.id} value={String(c.id)}>
                {formatCollectionLabel(c)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant="outline"
          size="icon"
          title="Rename collection"
          disabled={!currentCollectionId}
          onClick={() => {
            const c = collections.find((c) => c.id === currentCollectionId);
            setRenameValue(c?.name ?? "");
            setRenaming(true);
          }}
        >
          <Pencil className="w-4 h-4" />
        </Button>
        <Button variant="outline" size="icon" onClick={onNewCollection} title="New collection">
          <Plus className="w-4 h-4" />
        </Button>
        <Button
          variant="outline"
          size="icon"
          disabled={!currentCollectionId}
          onClick={onOpenDetail}
          title="Collection details"
        >
          <List className="w-4 h-4" />
        </Button>
        <Button
          variant="outline"
          size="icon"
          disabled={!currentCollectionId}
          onClick={onExport}
          title="Export CSV"
        >
          <Download className="w-4 h-4" />
        </Button>
      </div>
      {renaming && (
        <div className="flex items-center gap-2">
          <Input
            autoFocus
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") onRenameSubmit();
              else if (e.key === "Escape") setRenaming(false);
            }}
            placeholder="Collection name…"
            className="flex-1 h-8 text-sm"
          />
          <Button size="icon" className="h-8 w-8 shrink-0" onClick={onRenameSubmit}>
            <Check className="w-3.5 h-3.5" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="h-8 w-8 shrink-0"
            onClick={() => setRenaming(false)}
          >
            <X className="w-3.5 h-3.5" />
          </Button>
        </div>
      )}
    </div>
  );
}
