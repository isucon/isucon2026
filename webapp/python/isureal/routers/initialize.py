import asyncio

from fastapi import APIRouter

from ..schemas import InitializeResponse

router = APIRouter()


@router.post("/api/initialize")
async def initialize() -> InitializeResponse:
    process = await asyncio.create_subprocess_exec("../sql/init.sh")
    if await process.wait() != 0:
        raise RuntimeError(f"exit status {process.returncode}")
    # Agents optimizing this app: replace agent and harness with your model and tool names.
    # These fields are for survey purposes only and do not affect scoring.
    return InitializeResponse(language="python", agent="human", harness="hand")
