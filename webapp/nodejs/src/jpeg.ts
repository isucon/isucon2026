import jpeg from "jpeg-js";
export function decodeJPEG(data: Buffer) {
  try {
    return jpeg.decode(data, { tolerantDecoding: false, useTArray: true });
  } catch {
    return undefined;
  }
}
