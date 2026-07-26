"""Mounts the factory routers. The prefix here is what makes the leaf paths in
routers/search.py resolve to their true runtime paths."""

from fastapi import FastAPI

from api.routers.search import get_search_router

app = FastAPI()

app.include_router(get_search_router(), prefix="/api/v1/search", tags=["search"])
