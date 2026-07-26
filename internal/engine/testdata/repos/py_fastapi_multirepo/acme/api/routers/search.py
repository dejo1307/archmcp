"""Router factory: the router is built INSIDE the function and the handlers are
nested defs, the dominant FastAPI idiom. Module-level statement walking never
reaches these decorators, so before v133 this file emitted no route facts at all.
The paths here are leaves — "/api/v1/search" is composed from client.py's mount."""

from fastapi import APIRouter


def get_search_router() -> APIRouter:
    router = APIRouter()

    @router.get("/results")
    async def results():
        return []

    @router.post("/")
    async def search(payload: dict):
        return {}

    return router
