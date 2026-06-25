"""API layer that depends on the db module."""

from app.db import get_user


def handler(user_id):
    """Look up a user via the db layer."""
    return get_user(user_id)
