import io

from PIL import Image
from starlette.concurrency import run_in_threadpool


def _valid_jpeg(data: bytes, max_side: int) -> bool:
    try:
        with Image.open(io.BytesIO(data), formats=["JPEG"]) as image:
            if image.width > max_side or image.height > max_side:
                return False
            image.load()
            return True
    except OSError, SyntaxError, ValueError, Image.DecompressionBombError:
        return False


async def valid_jpeg(data: bytes, max_side: int) -> bool:
    return await run_in_threadpool(_valid_jpeg, data, max_side)
