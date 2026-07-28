import { create } from "@bufbuild/protobuf";
import { aiServiceClient } from "@/connect";
import { GenerateImageRequestSchema } from "@/types/proto/api/v1/ai_service_pb";

export interface GeneratedImage {
  file: File;
  model: string;
  generationId: string;
  settlementStatus: string;
  cost: string;
  credits: string;
}

export const imageGenerationService = {
  async generateImage(prompt: string, aspectRatio: string): Promise<GeneratedImage> {
    const response = await aiServiceClient.generateImage(
      create(GenerateImageRequestSchema, {
        prompt,
        aspectRatio,
      }),
    );
    if (response.content.length === 0 || !response.contentType.startsWith("image/")) {
      throw new Error("Image generation returned invalid image data");
    }

    const content = response.content.slice().buffer as ArrayBuffer;
    const file = new File([content], response.filename || "nano-banana-pro.png", {
      type: response.contentType,
    });
    return {
      file,
      model: response.model,
      generationId: response.generationId,
      settlementStatus: response.settlementStatus,
      cost: response.cost,
      credits: response.credits,
    };
  },
};
