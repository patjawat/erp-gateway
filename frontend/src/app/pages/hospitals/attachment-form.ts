export function attachmentSelectionError(
  file: Pick<File, "name" | "size">,
): string {
  if (file.size < 1) return "ไฟล์ว่าง กรุณาเลือกไฟล์ที่มีข้อมูล";
  if (file.size > 10 * 1024 * 1024) return "ไฟล์แนบต้องมีขนาดไม่เกิน 10 MB";
  if (!/\.(pdf|jpe?g|png|gif|webp)$/i.test(file.name))
    return "รองรับ PDF, JPEG, PNG, GIF และ WebP";
  return "";
}
