"""Data-access layer for the sample app."""


def get_user(user_id):
    """Return a fake user record by id."""
    return {"id": user_id, "name": "sample"}
