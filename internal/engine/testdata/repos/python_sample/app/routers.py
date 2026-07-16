"""Router factory: handlers are nested defs (closures), the dominant FastAPI
idiom. Their calls must be credited to the enclosing factory symbol (v117) —
without the nested-scope walk, _format_user reads as dead."""

from app.db import get_user


def _format_user(user):
    """Reached only from the nested handler below."""
    return {"user": user}


def _dead_helper(user):
    """Referenced by nobody — must stay an orphan (true-positive pin)."""
    return user


def get_user_router():
    def handler(user_id):
        return _format_user(get_user(user_id))

    return handler
