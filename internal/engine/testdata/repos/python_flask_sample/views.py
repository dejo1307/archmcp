"""Blueprint routing and a Flask-AppBuilder @expose view.

@bp.route pins Blueprint detection; @expose (no receiver dot) pins the
Flask-AppBuilder idiom that is invisible to a receiver-qualified regex. Both emit
bare leaf paths — url_prefix / route_base folding is tracked as GAP-PY-06.
"""

from flask import Blueprint
from flask_appbuilder import BaseView, expose

admin_bp = Blueprint("admin", __name__, url_prefix="/admin")


@admin_bp.route("/ping")
def ping():
    """Blueprint route → detected like @app.route, defaults to GET."""
    return {}


class HealthView(BaseView):
    """Flask-AppBuilder view: @expose methods carry the routes."""

    route_base = "/api"

    @expose("/version")
    def version(self):
        return {"version": "1"}

    @expose("/state", methods=["GET", "POST"])
    def state(self):
        return {}
