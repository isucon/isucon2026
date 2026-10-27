import asyncio

from fastapi import APIRouter

from ..schemas import InitializeResponse

router = APIRouter()


@router.post("/api/initialize")
async def initialize() -> InitializeResponse:
    process = await asyncio.create_subprocess_exec("../sql/init.sh")
    if await process.wait() != 0:
        raise RuntimeError(f"exit status {process.returncode}")
    return InitializeResponse(language="python")
