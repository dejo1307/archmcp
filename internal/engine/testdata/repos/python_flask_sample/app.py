"""Flask app: classic @app.route and Flask 2.0 @app.get shorthand.

Pins GAP-PY-01: routes are detected (were 0 before v109), and the @app.get verb
shorthand is labeled framework="flask" — not "fastapi" — because this project's
requirements.txt declares Flask and not FastAPI.
"""

from flask import Flask

from views import admin_bp

app = Flask(__name__)
app.register_blueprint(admin_bp)


@app.route("/users", methods=["GET", "POST"])
def users():
    """Classic Flask route with an explicit methods= list → one fact per verb."""
    return {}


@app.get("/health")
def health():
    """Flask 2.0 verb shorthand → framework flask, not fastapi."""
    return {"status": "ok"}
