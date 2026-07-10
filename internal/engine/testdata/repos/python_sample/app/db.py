"""Data-access layer for the sample app."""


def get_user(user_id):
    """Return a fake user record by id."""
    return {"id": user_id, "name": "sample"}


def get_path(node_id):
    """Walk a parent chain: `while True` repeats, so the lookup stays an N+1 candidate."""
    while True:
        get_user(node_id)


def seed():
    """Every in-loop call is inside a constant loop → calls_in_scaling_loop is empty, not absent."""
    for c in ("a", "b"):
        get_user(c)
