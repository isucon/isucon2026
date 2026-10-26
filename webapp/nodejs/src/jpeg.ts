import jpeg from "jpeg-js";
// Fully decodes the image like Go's image/jpeg.Decode; undefined when it is
// not a valid JPEG.
export function decodeJPEG(data: Buffer) {
  try {
    return jpeg.decode(data, { tolerantDecoding: false, useTArray: true });
  } catch {
    return undefined;
  }
}
