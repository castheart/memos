import { LoaderIcon, WandSparklesIcon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useTranslate } from "@/utils/i18n";

const ASPECT_RATIO_VALUES = ["1:1", "4:3", "3:4", "16:9", "9:16"] as const;

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  isGenerating: boolean;
  onGenerate: (prompt: string, aspectRatio: string) => Promise<void>;
}

export const ImageGenerationDialog = ({ open, onOpenChange, isGenerating, onGenerate }: Props) => {
  const t = useTranslate();
  const [prompt, setPrompt] = useState("");
  const [aspectRatio, setAspectRatio] = useState<(typeof ASPECT_RATIO_VALUES)[number]>("1:1");
  const aspectRatioOptions = useMemo(
    () =>
      ASPECT_RATIO_VALUES.map((value) => ({
        value,
        label: t(`editor.image-generation.aspect-${value.replace(":", "-")}` as Parameters<typeof t>[0]),
      })),
    [t],
  );

  useEffect(() => {
    if (!open) {
      setPrompt("");
      setAspectRatio("1:1");
    }
  }, [open]);

  const handleGenerate = () => {
    const trimmedPrompt = prompt.trim();
    if (!trimmedPrompt || isGenerating) {
      return;
    }
    void onGenerate(trimmedPrompt, aspectRatio);
  };

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => !isGenerating && onOpenChange(nextOpen)}>
      <DialogContent size="lg">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <span className="flex size-9 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <WandSparklesIcon className="size-5" />
            </span>
            <div>
              <DialogTitle>{t("editor.image-generation.title")}</DialogTitle>
              <DialogDescription className="mt-1">{t("editor.image-generation.description")}</DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="grid gap-2">
            <Label htmlFor="image-generation-prompt">{t("editor.image-generation.prompt-label")}</Label>
            <Textarea
              id="image-generation-prompt"
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder={t("editor.image-generation.prompt-placeholder")}
              maxLength={8000}
              rows={6}
              disabled={isGenerating}
              className="resize-y"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("editor.image-generation.aspect-ratio")}</Label>
            <Select
              value={aspectRatio}
              items={aspectRatioOptions}
              onValueChange={(value) => setAspectRatio(value as (typeof ASPECT_RATIO_VALUES)[number])}
              disabled={isGenerating}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {aspectRatioOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <p className="text-xs text-muted-foreground">{t("editor.image-generation.model")}</p>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={isGenerating}>
            {t("common.cancel")}
          </Button>
          <Button onClick={handleGenerate} disabled={!prompt.trim() || isGenerating}>
            {isGenerating ? <LoaderIcon className="size-4 animate-spin" /> : <WandSparklesIcon className="size-4" />}
            {t(isGenerating ? "editor.image-generation.generating" : "editor.image-generation.generate")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};
