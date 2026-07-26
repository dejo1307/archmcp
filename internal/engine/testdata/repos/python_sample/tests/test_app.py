"""Exercises production code and assembles a throwaway app.

The include_router below mounts a router at a TEST-ONLY prefix. It must never
reach the production route graph: the fixture would otherwise manufacture a
"/mounted-by-test" endpoint the service does not serve. The golden pins its
absence while still recording the references this file makes.
"""

import pytest
from fastapi import FastAPI

from app.api import handler
from app.routers import get_user_router
from app.tested_only import verify_checksum


@pytest.fixture
def app():
    app = FastAPI()
    app.include_router(get_user_router(), prefix="/mounted-by-test")
    return app


def test_handler():
    assert handler(1) is not None


def test_checksum():
    assert verify_checksum("abc") == 3
