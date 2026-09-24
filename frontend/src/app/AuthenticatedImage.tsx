import { useEffect, useState, type ImgHTMLAttributes } from 'react';
import { apiFetch } from '../api/client';

type Props = Omit<ImgHTMLAttributes<HTMLImageElement>, 'src'> & { src: string };

export function AuthenticatedImage({ src, ...props }: Props): React.JSX.Element {
  const [objectUrl, setObjectUrl] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    let nextObjectUrl: string | undefined;
    setObjectUrl(undefined);
    void apiFetch(src.replace(/^\/api/, ''), { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`Image request failed with status ${response.status}`);
        return response.blob();
      })
      .then((blob) => {
        nextObjectUrl = URL.createObjectURL(blob);
        setObjectUrl(nextObjectUrl);
      })
      .catch((error: unknown) => {
        if (!(error instanceof DOMException && error.name === 'AbortError')) setObjectUrl(undefined);
      });
    return () => {
      controller.abort();
      if (nextObjectUrl) URL.revokeObjectURL(nextObjectUrl);
    };
  }, [src]);

  return <img {...props} src={objectUrl} aria-busy={!objectUrl} />;
}
