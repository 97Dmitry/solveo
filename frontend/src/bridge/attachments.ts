export const maxImageBytes = 32 * 1024 * 1024;

// The backend validates actual format and dimensions. Reject large Files before
// FileReader/base64 makes additional copies in the renderer and Wails bridge.
export function imageBase64(file: File): Promise<string> {
  if (file.size === 0) return Promise.reject(new Error("Изображение пустое"));
  if (file.size > maxImageBytes)
    return Promise.reject(new Error("Изображение превышает лимит 32 МиБ"));
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("Не удалось прочитать файл изображения"));
    reader.onabort = () => reject(new Error("Чтение изображения отменено"));
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("Не удалось прочитать файл изображения"));
        return;
      }
      const comma = reader.result.indexOf(",");
      if (comma < 0) reject(new Error("Не удалось прочитать файл изображения"));
      else resolve(reader.result.slice(comma + 1));
    };
    reader.readAsDataURL(file);
  });
}
