import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { AttachmentSchema } from "@/types/proto/api/v1/attachment_service_pb";
import { getAttachmentThumbnailUrl, getAttachmentUrl } from "@/utils/attachment";

describe("attachment URLs", () => {
  it("uses a CDN external link for image source and thumbnail rendering", () => {
    const attachment = create(AttachmentSchema, {
      name: "attachments/image",
      filename: "image.png",
      type: "image/png",
      externalLink: "https://cdn.anyhostcloud.com/project/image.png",
    });

    expect(getAttachmentUrl(attachment)).toBe(attachment.externalLink);
    expect(getAttachmentThumbnailUrl(attachment)).toBe(attachment.externalLink);
  });

  it("keeps the authenticated file route for images without an external link", () => {
    const attachment = create(AttachmentSchema, {
      name: "attachments/image",
      filename: "image.png",
      type: "image/png",
    });

    expect(getAttachmentThumbnailUrl(attachment)).toContain("/file/attachments/image/image.png?thumbnail=true");
  });
});
