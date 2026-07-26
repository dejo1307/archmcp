"""Calls a function imported through a package __init__ re-export."""

from app.handlers import process_payload


def run(payload):
    return process_payload(payload)
